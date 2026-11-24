# 登录态、身份与多身份对照

## 一、微信登录链路

1. 小程序 `wx.login()` 得临时 `code`；
2. 前端把 code 发给业务后端；
3. 后端用 `AppSecret` 调微信 `jscode2session`：
   `https://api.weixin.qq.com/sns/jscode2session?appid=..&secret=..&js_code=..&grant_type=authorization_code`
4. 返回 `openid`、`unionid`（若绑定开放平台）、`session_key`；
5. 后端签发自己的 token/session，后续接口用它认证。

### 关键判断

- AppSecret 泄露 = 可自行换 openid（第 3 类），是最高价值发现之一。
- `session_key` 用于解密手机号等敏感数据，不应下发前端。

## 二、平台登录态复用

- `session_login`：把登录请求跑一次，cookie/token 进**任务级 cookie jar**，后续
  `http_request` 跨意图复用。
- `auth_request`：用资产上登记的凭据组，自动组带认证的请求（A/B 两套凭据）。
- 多身份矩阵：
  - A：普通用户 1
  - B：普通用户 2（验证水平越权）
  - 管理员（如授权，验证纵向越权）
  - 未登录（验证未授权）

## 三、身份参数与越权模式

小程序接口常见身份参数：

- `openid` / `unionid` / `userId` / `uid` / `memberId`
- token 里编码的身份字段（JWT payload 可解 base64 看）

高发缺陷：

- 服务端信任请求体里的 openid，而不是从 token 推导；
- 列表/详情接口只按 id 查询，不校验归属；
- 导出/下载接口沿用同一个 id，未二次鉴权；
- 管理端接口只在前端隐藏，未做服务端角色校验。

验证方法：A 的 token + B 的资源 id → 若返回 B 的数据即水平越权。证据必须 A/B 对照，
并保留资源 id 的可核对特征（脱敏）。

## 四、手机号与敏感数据

- `getPhoneNumber` 返回 `encryptedData` + `iv`，服务端用 `session_key` AES 解密。
- 缺陷：解密接口不校验 code/session_key、允许解任意密文、把明文手机号返回前端。
- 验证只做一条，证明可解密/可越权即止；不在报告里写完整手机号。

## 五、边界

- 只使用授权测试账号；不撞库、不批量注册。
- 不导出他人隐私数据；验证到「能读到一条非本人数据」即停。
- token/secret 只进运行时配置与证据附件，不写进对话或报告正文。
