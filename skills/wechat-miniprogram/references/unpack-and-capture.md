# 取包、解包与抓包

## 一、取包

### Android（测试机/模拟器）

- 路径（微信主包缓存）：`/data/data/com.tencent.mm/MicroMsg/<hash>/appbrand/pkg/`
- 需要 root 或 `adb backup`；不同版本路径可能变化，用 `scripts/collect_android.sh` 自动探测。
- 命令示例：
  ```bash
  adb shell "su -c 'ls -l /data/data/com.tencent.mm/MicroMsg/*/appbrand/pkg/'"
  adb pull /data/data/com.tencent.mm/MicroMsg/<hash>/appbrand/pkg/ ./pkg/
  ```
- 新版微信可能对包加密或改名，记录版本与失败原因，别硬猜。

### PC 微信（Windows）

- `WeChat Files/Applet/<appid>/<version>/` 下常见 `__APP__.wxapkg` 与分包。
- 用 `scripts/collect_windows.ps1` 复制到任务目录；也可手动搜索 `*.wxapkg`。

### iOS

- 越狱设备路径类似 `.../WeChat/.../appbrand/pkg/`；未越狱难以直接取包。
- 可取的是抓包与 H5 侧资源，记录限制。

## 二、解包

### 平台工具

```
miniprogram_unpack(input_path="pkg/__APP__.wxapkg", output_dir="unpacked")
```

- 支持标准 v1 明文与单字节 XOR 包；返回文件清单、app-config/project.config、sourcemap。
- 不支持的变体会明确报错。

### 外部工具（原生工具失败时）

- `wxappUnpacker`（Node）：`node wuWxapkg.js __APP__.wxapkg`
- `unveilr`（Python/Go 实现，社区维护）：`unveilr wxapkg __APP__.wxapkg -o out`
- 解包后同样用 `miniprogram_audit` 做静态审计。

## 三、抓包

- 平台 MITM 记录代理已就绪：Bash 子进程自动带 `HTTP_PROXY/HTTPS_PROXY`，
  `http_request` 也走同一出口；流量会进平台留痕。
- 测试机需安装平台 CA 证书（Android 7+ 需系统证书或测试包允许用户证书）。
- 小程序可能对关键接口做 SSL pinning：
  - 优先用测试包/开发版（通常关闭 pinning）；
  - 或在授权范围内用 Frida/objection 去 pinning（仅测试机、仅授权目标）；
  - 不要在生产/他人设备上做绕过。
- 抓包重点：登录、首页、列表、详情、下单/支付、个人中心、上传。

## 四、常见坑

- 解包成功但接口是动态拼接（baseURL + path）：在 `miniprogram_audit` 结果基础上人工/脚本拼接。
- 接口域名在运行时下发（配置中心）：抓包补全，静态清单只作参考。
- 包内多个分包（subpackage）：分别解包，别只看主包。
- sourcemap 存在时先还原源码，往往能直接看到鉴权逻辑与隐藏接口。
