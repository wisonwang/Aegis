# Aegis 发布与供应链说明

&gt; 目标：说明如何在本地准备发布资产，以及如何通过 GitHub Actions 触发正式 Release。

---

## 1. 当前发布面包含什么

当前仓库已经具备以下发布与供应链资产：

- CI 工作流：`.github/workflows/ci.yml`
- 标签发布工作流：`.github/workflows/release.yml`
- Release 模板：`.github/RELEASE_TEMPLATE.md`
- 本地构建归档脚本：`scripts/build_release_artifacts.sh`
- 校验和生成脚本：`scripts/generate_checksums.sh`
- SBOM 生成脚本：`scripts/generate_sbom.sh`
- 漏洞扫描脚本：`scripts/scan_vulnerabilities.sh`
- 签名脚本：`scripts/sign_artifacts.sh`

---

## 2. 本地发布准备

### 2.1 最小检查

```bash
make ci-smoke
```

### 2.2 生成发布归档

```bash
make release-artifacts VERSION=v0.7.0
```

会生成：

- `dist/aegis_v0.7.0_linux_amd64.tar.gz`
- `dist/aegis_v0.7.0_linux_arm64.tar.gz`
- `dist/aegis_v0.7.0_darwin_amd64.tar.gz`
- `dist/aegis_v0.7.0_darwin_arm64.tar.gz`
- `dist/checksums.txt`

### 2.3 生成 SBOM

```bash
make release-sbom
```

默认输出：

- `dist/aegis-sbom.spdx.json`

前置条件：

- 本机已安装 `syft`
- 或本机可正常访问 Docker daemon，以便通过 `anchore/syft` 容器生成

### 2.4 漏洞扫描

```bash
make release-scan
```

默认输出：

- `dist/aegis-vulns-trivy.json`

前置条件：

- 本机已安装 `trivy`
- 或本机可正常访问 Docker daemon
- 也可回退到 `grype`

### 2.5 签名（可选）

```bash
make release-sign VERSION=v0.7.0
```

会生成：

- `dist/aegis_v0.7.0_linux_amd64.tar.gz.sig`
- `dist/aegis_v0.7.0_linux_amd64.tar.gz.pem`
- ... (每个归档 + SBOM + 扫描报告 + checksums 都有签名)

前置条件：

- 本机已安装 `cosign`

### 2.6 一次性准备完整发布资产

```bash
make release-prepare VERSION=v0.7.0
```

该命令会执行：

- `make ci-smoke`
- release archives
- SBOM
- 漏洞扫描
- checksums

---

## 3. GitHub Release 流程

仓库已配置标签触发工作流：

- `.github/workflows/release.yml`

触发方式：

```bash
git tag v0.7.0
git push origin v0.7.0
```

工作流会自动执行：

- `make ci-smoke`
- 构建多平台归档
- 生成 SBOM
- Trivy 漏洞扫描
- 生成 `checksums.txt`
- 使用 cosign 签名所有发布资产
- 创建 GitHub Release
- 上传 `dist/` 下的发布资产（包括签名和证书）

---

## 4. 当前产物说明

### 4.1 归档文件

适合用户直接下载并解压运行。

每个归档内包含：

- `aegis`
- `LICENSE`
- `NOTICE`
- `README.md`

### 4.2 Checksums

`checksums.txt` 用于用户校验下载文件完整性。

### 4.3 SBOM

`aegis-sbom.spdx.json` 用于供应链、法务与安全审查。

### 4.4 漏洞扫描报告

`aegis-vulns-trivy.json` 记录了 Trivy 扫描结果。

### 4.5 签名与证书

每个发布资产（归档、SBOM、扫描报告、checksums）都有对应的：

- `*.sig` - cosign 签名
- `*.pem` - cosign 证书

验证方式：

```bash
cosign verify-blob &lt;artifact&gt; --signature &lt;artifact&gt;.sig --certificate &lt;artifact&gt;.pem
```

---

## 5. 自动化依赖清单与许可说明

当前仓库已包含：

- `NOTICE` - 第三方许可汇总
- `docs/third-party-notices.md` - 详细第三方说明

SBOM 中也包含依赖信息，可配合 `syft` / `trivy` 进一步审计。

---

## 6. 推荐发布前动作

每次正式发布前，建议按下面顺序执行：

1. 更新 `CHANGELOG.md`
2. 检查 `README.md` / `examples/` / `docs/`
3. 执行 `make release-prepare VERSION=vX.Y.Z`
4. 按 `.github/RELEASE_TEMPLATE.md` 准备 release notes
5. 推送 tag，触发 GitHub Release

如果本地没有 `syft` 且 Docker 未启动，可以先执行：

```bash
make ci-smoke
make release-artifacts VERSION=vX.Y.Z
```

再在 CI / GitHub Release 工作流中生成 SBOM。

---

## 7. 关联文档

- [release-policy.md](file:///Users/vincent/workspace/fosun/datahub/docs/release-policy.md)
- [third-party-notices.md](file:///Users/vincent/workspace/fosun/datahub/docs/third-party-notices.md)
- [support-policy.md](file:///Users/vincent/workspace/fosun/datahub/docs/support-policy.md)
- [launch.md](file:///Users/vincent/workspace/fosun/datahub/docs/launch.md)
- [production-readiness-checklist.md](file:///Users/vincent/workspace/fosun/datahub/docs/production-readiness-checklist.md)
- [commercial-packaging-plan.md](file:///Users/vincent/workspace/fosun/datahub/docs/commercial-packaging-plan.md)
