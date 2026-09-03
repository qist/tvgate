import { useEffect } from "react";
import { Outlet, useNavigate } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { useTheme } from "@/hooks/use-theme";
import { Moon, Sun } from "lucide-react";

/** 空白布局：登录页 / 全屏页 */
export function BlankLayout() {
  const navigate = useNavigate();
  const { theme, setTheme } = useTheme();

  useEffect(() => {
    // 已登录则回到首页
    fetch(new URL("auth-status", window.location.href.split("#")[0]).toString(), { credentials: "same-origin" })
      .then((r) => r.ok && navigate("/", { replace: true }))
      .catch(() => {});
  }, [navigate]);

  return (
    <div className="relative flex min-h-screen items-center justify-center bg-background p-4">
      <Button
        variant="ghost"
        size="icon"
        className="absolute right-4 top-4"
        onClick={() => setTheme(theme === "dark" ? "light" : "dark")}
      >
        {theme === "dark" ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
      </Button>
      <Outlet />
    </div>
  );
}