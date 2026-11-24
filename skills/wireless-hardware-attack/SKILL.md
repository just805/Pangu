---
name: wireless-hardware-attack
description: 目标含 WiFi/蓝牙/BLE/无线设备、IoT 或物理硬件时调用——无线侦察、WPA/WPS 攻击路径、蓝牙特征读写、固件提取与硬件接口（UART/JTAG）测试，以及从固件回到服务端的验证路径。
tags:
  cwe: [CWE-321, CWE-798, CWE-1188, CWE-306, CWE-319]
  attack: [T1465, T1557, T1552.001]
  class: [wireless, hardware, iot, firmware, ble]
---

# 无线与硬件渗透速查

## 与本平台的衔接

| 场景 | 用平台能力 |
|---|---|
| 无线侦察/抓包 | Bash 跑 `iw`/`airodump-ng`/`bettercap`（宿主 Kali 工具，环境页可按需装） |
| 固件下载/升级包 | `http_request` 抓升级接口；样本 `save_evidence` 归档 |
| 固件解包/模拟 | Bash 跑 `binwalk -e`/`firmadyne`/`qemu`（隔离网络） |
| 固件里发现的后端接口 | 交给 `http_request` + `auth_request` + `diff_responses` 做越权/未授权 |
| 硬编码密钥/口令 | 全文写 `record_fact`，再用 `http_request`/`session_login` 验证能否登录 |
| 证据 | `save_evidence`（截图、固件哈希、串口日志）；`report_finding` 附命令与输出 |

## 一、WiFi

### 侦察

```bash
iw dev                          # 网卡与模式
airmon-ng start wlan0           # 监听模式（确认网卡支持）
airodump-ng wlan0mon            # AP/客户端/加密类型/信道
wash -i wlan0mon                # WPS 开启情况
```

### WPA/WPA2

1. 锁定目标 BSSID + 信道，抓 4 次握手：
   `airodump-ng -c <ch> --bssid <bssid> -w cap wlan0mon`
2. 可主动去认证加速（**仅在授权窗口，少量**）：`aireplay-ng -0 3 -a <bssid> -c <client> wlan0mon`
3. 离线破解：`aircrack-ng -w rockyou.txt cap-01.cap`；或 `hashcat -m 22000`。

### WPS

- `reaver -i wlan0mon -b <bssid> -vv`（先确认授权；WPS 锁定会拖很久，设超时）。
- `bully` 作为替代。

### 企业级 802.1X

- EAP 降级 / evil twin / RADIUS 伪造属高风险，**只在明确授权的演练**做；
- 结论口径：拿到明文口令或成功接入内网才算 L3；仅抓到握手是 L2。

## 二、蓝牙 / BLE

```bash
bluetoothctl scan on
hcitool lescan
gatttool -b <mac> --primary
gatttool -b <mac> --char-read -a <handle>
gatttool -b <mac> --char-write-req -a <handle> -n <hex>
```

- 未授权可写特征 → 设备控制（门锁/传感器/玩具），是常见高危；
- 默认 PIN（0000/1234）、配对模式无确认、可枚举 MAC；
- 证据：特征句柄 + 写入值 + 设备实际反应（截图/串口日志）。

## 三、IoT / 固件

### 获取固件

- 官网下载 / 升级接口抓包（`http_request` 或 MITM 代理）；
- 设备 Flash 读取（SPI/CH341A）、`binwalk -e firmware.bin` 解包；
- 记录固件版本、哈希、来源，`save_evidence`。

### 解包后优先看

- `/etc/shadow`、`/etc/passwd`、硬编码密钥/证书；
- `squashfs-root/www`、`/cgi-bin`、管理接口；
- 调试入口：telnet/ssh/串口/`/debug`；
- 启动脚本里的云端点、MQTT broker、升级校验逻辑。

### 模拟（隔离网络）

```bash
# 仅隔离环境；不要接生产网
sudo firmadyne.sh ./firmware.bin
# 或手工 qemu 起 mips/arm
```

### 硬件接口

- UART：识别 TX/RX/GND（万用表/逻辑分析仪），`picocom -b 115200 /dev/ttyUSB0`；**先只读**；
- JTAG/SWD：`openocd` + 对应配置文件；只在自有设备上做；
- 常见口令：`root/root`、`admin/admin`、空口令。

## 四、从硬件回到服务端

- 固件里常带后端 API、设备密钥、升级签名公钥；
- 用 `http_request` 验证接口是否未授权/可越权（设备 ID 替换）；
- 设备配网/绑定接口常见 IDOR（换设备 ID 控制他人设备）；
- 接口清单落 `record_fact`，交给 `api-security-testing` 继续。

## 五、证据与验证纪律

- 无线：抓包文件 + 破解结果；结论写清「能否接入/能否解密」；
- BLE：特征读写请求 + 设备反应；
- 固件：哈希 + 解包清单 + 关键文件片段；
- 硬件：串口/JTAG 日志 + 操作前后状态；
- 未实际验证的推断最多 medium，并注明「未在真实设备验证」。

## 六、边界

- 无线攻击会干扰他人，仅在明确授权的演练/测试窗口做；
- 不做 deauth 泛洪、不干扰生产无线、不攻击非授权 AP；
- 固件/硬件测试在隔离环境，不接生产网络；
- 不读取/导出设备上的真实用户数据；验证到「能控制/能读一条」即止。
