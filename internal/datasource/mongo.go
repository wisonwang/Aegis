package datasource

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/wisonwang/aegis/internal/store"
)

// mongoConnector serves MongoDB (and compatible APIs). Queries arrive as a JSON
// document describing the collection, filter, projection, sort and limit. The
// proxy applies table/column governance BEFORE the connector is called, so the
// connector only executes the already-governed query.
type mongoConnector struct{}

func (c *mongoConnector) Kind() string { return "mongo" }

func (c *mongoConnector) Open(ds *store.DataSource) (Session, error) {
	client, err := mongo.Connect(context.Background(), options.Client().ApplyURI(ds.DSN))
	if err != nil {
		return nil, fmt.Errorf("connect mongo %q: %w", ds.Name, err)
	}
	dbName := mongoDBName(ds.DSN)
	return &mongoSession{client: client, dbName: dbName}, nil
}

func mongoDBName(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return "admin"
	}
	db := strings.TrimPrefix(u.Path, "/")
	if i := strings.IndexByte(db, '?'); i >= 0 {
		db = db[:i]
	}
	if db == "" {
		return "admin"
	}
	return db
}

type mongoSession struct {
	client *mongo.Client
	dbName string
}

type mongoQuery struct {
	Collection string          `json:"collection"`
	Filter     json.RawMessage `json:"filter"`
	Projection json.RawMessage `json:"projection"`
	Sort       json.RawMessage `json:"sort"`
	Limit      *int64          `json:"limit"`
}

// mongoWriteQuery carries a mutating operation. op is one of
// insert|update|delete; the remaining fields are op-specific.
type mongoWriteQuery struct {
	Op        string          `json:"op"`
	Collection string         `json:"collection"`
	Document  json.RawMessage `json:"document"`  // insert (single)
	Documents json.RawMessage `json:"documents"` // insertMany
	Filter    json.RawMessage `json:"filter"`    // update / delete
	Update    json.RawMessage `json:"update"`    // update
	Multi     bool            `json:"multi"`     // update/delete many
}

func (s *mongoSession) Exec(ctx context.Context, payload QueryPayload) (*RawResult, int64, error) {
	var q mongoQuery
	if err := json.Unmarshal(payload.Raw, &q); err != nil {
		return nil, 0, fmt.Errorf("invalid mongo query: %w", err)
	}
	if q.Collection == "" {
		return nil, 0, fmt.Errorf("mongo query requires a 'collection' field")
	}
	coll := s.client.Database(s.dbName).Collection(q.Collection)

	filter := bson.M{}
	if len(q.Filter) > 0 {
		if err := json.Unmarshal(q.Filter, &filter); err != nil {
			return nil, 0, fmt.Errorf("invalid mongo filter: %w", err)
		}
	}

	opts := options.Find()
	if len(q.Projection) > 0 {
		var proj bson.M
		if err := json.Unmarshal(q.Projection, &proj); err != nil {
			return nil, 0, fmt.Errorf("invalid mongo projection: %w", err)
		}
		opts.SetProjection(proj)
	}
	if len(q.Sort) > 0 {
		var sort bson.M
		if err := json.Unmarshal(q.Sort, &sort); err != nil {
			return nil, 0, fmt.Errorf("invalid mongo sort: %w", err)
		}
		opts.SetSort(sort)
	}
	if q.Limit != nil {
		opts.SetLimit(*q.Limit)
	}

	cur, err := coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, 0, fmt.Errorf("mongo find: %w", err)
	}
	defer cur.Close(ctx)

	var docs []map[string]interface{}
	if err := cur.All(ctx, &docs); err != nil {
		return nil, 0, fmt.Errorf("mongo decode: %w", err)
	}

	colSet := map[string]bool{}
	for _, d := range docs {
		for k := range d {
			colSet[k] = true
		}
	}
	cols := make([]string, 0, len(colSet))
	for k := range colSet {
		cols = append(cols, k)
	}
	sort.Strings(cols)

	raw := &RawResult{Columns: cols, Rows: make([]map[string]interface{}, 0, len(docs))}
	for _, d := range docs {
		row := make(map[string]interface{}, len(cols))
		for _, c := range cols {
			row[c] = normalizeCell(d[c])
		}
		raw.Rows = append(raw.Rows, row)
	}
	return raw, 0, nil
}

func (s *mongoSession) ListCollections(ctx context.Context) ([]string, error) {
	names, err := s.client.Database(s.dbName).ListCollectionNames(ctx, bson.M{})
	if err != nil {
		return nil, fmt.Errorf("mongo list collections: %w", err)
	}
	return names, nil
}

func (s *mongoSession) DescribeCollection(ctx context.Context, name string) ([]ColumnMeta, error) {
	coll := s.client.Database(s.dbName).Collection(name)
	var doc bson.M
	err := coll.FindOne(ctx, bson.M{}).Decode(&doc)
	if err != nil {
		// Empty collection: report no columns rather than failing introspection.
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, fmt.Errorf("mongo describe %q: %w", name, err)
	}
	out := make([]ColumnMeta, 0, len(doc))
	for k, v := range doc {
		out = append(out, ColumnMeta{Name: k, Type: mongoType(v)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func mongoType(v interface{}) string {
	switch x := v.(type) {
	case string:
		return "string"
	case bool:
		return "bool"
	case int32:
		return "int"
	case int64:
		return "long"
	case float64:
		return "double"
	case []interface{}:
		return "array"
	case map[string]interface{}, bson.M:
		return "object"
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%T", x)
	}
}

func (s *mongoSession) Close() error { return s.client.Disconnect(context.Background()) }

// Write runs a governed mutating operation. op is insert|update|delete.
func (s *mongoSession) Write(ctx context.Context, payload WritePayload) (int64, error) {
	var w mongoWriteQuery
	if err := json.Unmarshal(payload.Raw, &w); err != nil {
		return 0, fmt.Errorf("invalid mongo write: %w", err)
	}
	if w.Collection == "" {
		return 0, fmt.Errorf("mongo write requires 'collection'")
	}
	coll := s.client.Database(s.dbName).Collection(w.Collection)
	switch w.Op {
	case "insert":
		if len(w.Document) > 0 {
			var doc bson.M
			if err := json.Unmarshal(w.Document, &doc); err != nil {
				return 0, fmt.Errorf("invalid mongo document: %w", err)
			}
			res, err := coll.InsertOne(ctx, doc)
			if err != nil {
				return 0, fmt.Errorf("mongo insertOne: %w", err)
			}
			if res.InsertedID != nil {
				return 1, nil
			}
			return 0, nil
		}
		if len(w.Documents) > 0 {
			var docs []interface{}
			if err := json.Unmarshal(w.Documents, &docs); err != nil {
				return 0, fmt.Errorf("invalid mongo documents: %w", err)
			}
			res, err := coll.InsertMany(ctx, docs)
			if err != nil {
				return 0, fmt.Errorf("mongo insertMany: %w", err)
			}
			return int64(len(res.InsertedIDs)), nil
		}
		return 0, fmt.Errorf("mongo insert requires 'document' or 'documents'")
	case "update":
		var filter bson.M
		if len(w.Filter) > 0 {
			if err := json.Unmarshal(w.Filter, &filter); err != nil {
				return 0, fmt.Errorf("invalid mongo filter: %w", err)
			}
		}
		var update bson.M
		if len(w.Update) > 0 {
			if err := json.Unmarshal(w.Update, &update); err != nil {
				return 0, fmt.Errorf("invalid mongo update: %w", err)
			}
		}
		if w.Multi {
			res, err := coll.UpdateMany(ctx, filter, update)
			if err != nil {
				return 0, fmt.Errorf("mongo update: %w", err)
			}
			return res.ModifiedCount + res.UpsertedCount, nil
		}
		res, err := coll.UpdateOne(ctx, filter, update)
		if err != nil {
			return 0, fmt.Errorf("mongo update: %w", err)
		}
		return res.ModifiedCount + res.UpsertedCount, nil
	case "delete":
		var filter bson.M
		if len(w.Filter) > 0 {
			if err := json.Unmarshal(w.Filter, &filter); err != nil {
				return 0, fmt.Errorf("invalid mongo filter: %w", err)
			}
		}
		if w.Multi {
			res, err := coll.DeleteMany(ctx, filter)
			if err != nil {
				return 0, fmt.Errorf("mongo delete: %w", err)
			}
			return res.DeletedCount, nil
		}
		res, err := coll.DeleteOne(ctx, filter)
		if err != nil {
			return 0, fmt.Errorf("mongo delete: %w", err)
		}
		return res.DeletedCount, nil
	default:
		return 0, fmt.Errorf("unsupported mongo op %q", w.Op)
	}
}

// Count returns the number of documents matching a governed filter. Used by the
// proxy to enforce the affected-rows guard before an update/delete.
func (s *mongoSession) Count(ctx context.Context, payload QueryPayload) (int64, error) {
	var q mongoQuery
	if err := json.Unmarshal(payload.Raw, &q); err != nil {
		return 0, fmt.Errorf("invalid mongo count query: %w", err)
	}
	if q.Collection == "" {
		return 0, fmt.Errorf("mongo count requires 'collection'")
	}
	var filter bson.M
	if len(q.Filter) > 0 {
		if err := json.Unmarshal(q.Filter, &filter); err != nil {
			return 0, fmt.Errorf("invalid mongo filter: %w", err)
		}
	}
	n, err := s.client.Database(s.dbName).Collection(q.Collection).CountDocuments(ctx, filter)
	if err != nil {
		return 0, fmt.Errorf("mongo count: %w", err)
	}
	return n, nil
}
