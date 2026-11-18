// Next.js 16 middleware（原 middleware.ts）。
//
// 运行范围：只在 `next dev` 生效——静态导出（NEXT_EXPORT=1 next build）不包含
// middleware，生产内嵌模式下的页面守卫由「API 401 → 跳 /login」兜底。这里的
// 未登录重定向只是 dev 期的体验优化，不是安全边界；真正的边界在 Go 侧
// requireAuth（session cookie 为 HttpOnly，由后端 login/init 响应下发）。
import { NextResponse } from "next/server";
import type { NextRequest } from "next/server";

const AUTH_PAGES = ["/login", "/setup"];

export function proxy(request: NextRequest) {
  // Mock demo：无真实登录，放行所有页面（客户端 auth 守卫也会放行）。
  if (process.env.NEXT_PUBLIC_MOCK === "1") return NextResponse.next();

  const { pathname } = request.nextUrl;
  const token = request.cookies.get("pangu_token")?.value;
  const isAuthPage = AUTH_PAGES.some((p) => pathname === p || pathname.startsWith(`${p}/`));

  // 未登录 → 跳转登录页
  if (!token && !isAuthPage) {
    return NextResponse.redirect(new URL("/login", request.url));
  }

  // 已登录时访问登录/初始化页 → 跳转主界面
  if (token && isAuthPage) {
    return NextResponse.redirect(new URL("/function/tasks", request.url));
  }

  return NextResponse.next();
}

export const config = {
  // 跳过 Next.js 内部路由、API 路由、favicon 及 public/ 下的静态文件（含图片、字体等）
  matcher: ["/((?!_next/static|_next/image|favicon\\.ico|api/|.*\\.(?:png|jpg|jpeg|gif|webp|svg|ico|woff2?|ttf|otf)$).*)"],
};
