# 自动化与证据留痕

> 原则：自动化只做「发现与对照」，破坏性/批量操作一律手动且最小化。

## 一、静态审计

```
miniprogram_unpack(input_path="pkg/__APP__.wxapkg", output_dir="unpacked")
miniprogram_audit(path="unpacked")
```

把 `endpoints`、`secrets`、`cloud.functions`、`cloud.envs` 写进 `record_fact`，
作为后续接口测试的待办清单。

## 二、批量探活

- `http_probe`：一行一个 URL，批量拿状态/标题/长度，先筛可访问面。
- 对 `miniprogram_audit` 产出的接口按域名分组，先探活再逐个深测。

## 三、接口对照（越权/未授权）

用 `http_request` 做三组对照，结果写 `record_fact`：

1. 未登录：去掉 token/cookie；
2. A 身份：A token + A 资源 id；
3. 跨身份：A token + B 资源 id。

`diff_responses` 对比响应结构差异，快速判断是否越权。

## 四、参数枚举

- 对数字 id、订单号、userId 做**少量**递增/替换（每次 2–3 个），证明可越权即停。
- 不做批量爬取；不在生产做写操作。

## 五、证据模板

每条漏洞记录：

```
类别：#7 水平越权
请求：GET /api/order/detail?id=<B_id>
身份：A（普通用户）
响应：200，返回 B 的订单（订单号后四位 ****）
流量：<traffic_id>（平台绑定）
对照：A token + A id → A 数据；A token + B id → B 数据
```

## 六、脚本位置

- 取包：`${SKILL_DIR}/scripts/collect_android.sh`、`collect_windows.ps1`
- 自定义批量脚本可存 `save_script`，跨意图复用；不要在脚本里硬编码真实凭据。

## 七、禁止

- 不做短信/验证码轰炸、不做大规模并发、不批量导出用户数据。
- 不把 AppSecret/token/手机号明文写进对话或报告正文。
