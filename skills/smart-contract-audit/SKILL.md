---
name: smart-contract-audit
description: 目标含 Solidity/智能合约、Web3 DApp、链上资产相关系统时调用——常见合约漏洞（重入/预言机/权限/签名/升级）、slither/mythril/forge 工具链、链上只读验证与影响判定。
tags:
  cwe: [CWE-682, CWE-367, CWE-284, CWE-347, CWE-829, CWE-1104]
  attack: [T1195]
  class: [blockchain, solidity, smart-contract, web3, defi]
---

# 智能合约审计速查

## 与本平台的衔接

| 场景 | 用平台能力 |
|---|---|
| 合约源码获取 | Bash 跑 `cast`/浏览器 API；源码落任务目录 |
| 静态分析 | Bash 跑 `slither`/`mythril`/`aderyn` |
| 本地复现 | Bash 跑 `forge test`（fork 主网，只读） |
| 链上只读 | `cast call`/`cast logs`；RPC 用 `http_request` 可留痕 |
| 前端/密钥泄露 | `js_audit` 找私钥/助记词/RPC 端点；`miniprogram_audit` 同类 |
| 证据 | `save_evidence`（交易哈希、PoC 合约、fork 输出）；`report_finding` |
| 依赖 CVE | 合约依赖/Oracle/桥接组件用 `search_vulns` |

## 一、高频漏洞

| 漏洞 | 特征 | 验证 |
|---|---|---|
| 重入 | 先外部调用后改状态；缺 ReentrancyGuard | 构造攻击合约，fork 主网复现 |
| 权限缺失 | `onlyOwner` 遗漏、`initialize` 可重复调用 | 直接调受限函数看是否成功 |
| 访问控制错配 | `tx.origin` 鉴权、角色可自授 | 用合约/中间人调用验证 |
| 预言机操纵 | 现货价/单一 DEX 价做抵押定价 | 闪电贷操纵路径推演/fork 复现 |
| 签名重放 | 无 nonce/chainId/合约地址/过期 | 同一签名用两次 |
| 签名可延展 | `ecrecover` 未校验 s/v 或 malleability | 构造变形签名 |
| 越权转账 | `transferFrom` 缺 allowance 校验 | 本地复现 |
| 整数/精度 | 除法先于乘法、精度截断 | 边界值测试 |
| 随机数可预测 | `block.timestamp/blockhash` 做随机 | 本地复现 |
| delegatecall | 代理存储布局冲突/任意地址 | 存储槽对比 |
| 升级风险 | `upgradeTo` 无权限/无 timelock | 调升级函数 |
| 批准竞态 | ERC20 `approve` 竞态 | 双花场景推演 |

## 二、工具链

```bash
slither . --config slither.config.json     # 静态快速：reentrancy/arbitrary-send/controlled-delegatecall
mythril analyze contracts/ --execution-timeout 300   # 符号执行，慢
aderyn .                                    # Rust 实现的快速静态器
forge test --fork-url $RPC -vvv             # 本地 fork 复现（最可信）
```

- 无源码：`heimdall`/`panoramix` 反编译，先看 `upgrade`/`owner`/`transfer` 函数。
- 依赖：`search_vulns` 查 OpenZeppelin/桥/Oracle 版本 CVE。

## 三、链上只读验证

```bash
cast call <addr> "balanceOf(address)(uint256)" <user>
cast call <addr> "owner()(address)"
cast logs --address <addr> --from-block <n>
cast storage <addr> <slot>
```

- 未授权读链上公开数据**不算漏洞**；关注「业务逻辑可被任意人滥用/资产可被转走」。
- 证据：交易哈希、fork 测试输出、前后余额/状态对比。

## 四、DApp / Web3 前端

- 私钥/助记词硬编码在前端 bundle → 立即 `record_fact` + `save_evidence`；
- RPC 端点未授权/可 `eth_sendRawTransaction`；
- 签名钓鱼：`eth_sign` 任意消息、`permit` 无限授权、`setApprovalForAll`；
- 前端调用的合约地址与审计对象不一致（代理升级/钓鱼）；
- 用 `js_audit` 抽前端接口与密钥，`http_request` 验证 RPC/后端。

## 五、证据与验证纪律

- 只在测试网或本地 fork 验证；主网只做只读调用；
- 不发任何会转移资产的交易；
- 每条结论附：合约地址/函数、PoC、fork 结果或交易哈希；
- 仅代码推断未复现 → 最多 medium，注明「未复现」。

## 六、边界

- 不触碰真实用户资产、不发主网交易；
- 不利用漏洞转移/锁定他人资产；
- 发现可窃取资产的漏洞时立即停手并上报，只做最小只读证明。
