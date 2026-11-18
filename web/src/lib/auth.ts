// token 的浏览器侧载体 = HttpOnly cookie（由后端 login/init 响应以 Set-Cookie
// 下发，XSS 读不到、也不会进任何 JS 可写的存储）。前端只把 token 存在**内存**里：
// 登录后的 API 请求带 Authorization 头（走内存 token），页面刷新后内存清空，
// 此时同源的 fetch/EventSource 依旧自动携带 cookie 完成认证——同源请求默认
// credentials: same-origin，cookie 不区分端口（dev 直连 :8787 同样适用）。
//
// 历史实现把 token 落 localStorage + document.cookie：任意 XSS 都能偷走一个
// 7 天有效的 JWT，cookie 副本还供 Next middleware（静态导出下本就不运行）读取。
// 现在两层副本都移除；登出/清会话统一走 POST /api/auth/logout（后端权威清 cookie）。
let memoryToken: string | null = null;

export interface CurrentUser {
  id: string;
  name: string;
  username: string;
  email: string;
  avatar: string;
  role: string;
}

export const auth = {
  getToken(): string | null {
    if (typeof window === "undefined") return null;
    // Mock demo：无真实登录，返回一个假 token 让路由守卫放行、直接进主界面。
    if (process.env.NEXT_PUBLIC_MOCK === "1") return "mock-demo";
    return memoryToken;
  },

  setToken(token: string): void {
    memoryToken = token;
    // cookie 由后端响应下发（HttpOnly + SameSite=Lax），前端不再手写。
  },

  clearToken(): void {
    memoryToken = null;
  },

  // 从 JWT payload 的 sub 字段解析当前用户，仅用于展示，不做签名验证。
  // 刷新后内存 token 为空（HttpOnly cookie 读不到），返回 null——调用方
  // use-current-user 自带 PANGU fallback。
  getCurrentUser(): CurrentUser | null {
    const token = this.getToken();
    if (!token) return null;
    try {
      const parts = token.split(".");
      if (parts.length !== 3) return null;
      // base64url → base64
      const payload = JSON.parse(atob(parts[1].replace(/-/g, "+").replace(/_/g, "/")));
      const username: string = payload.sub ?? "PANGU";
      return { id: "1", name: username, username, email: "", avatar: "", role: "operator" };
    } catch {
      return null;
    }
  },
};
