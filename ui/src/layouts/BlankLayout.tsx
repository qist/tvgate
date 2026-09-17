import { Outlet } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { SelectBox } from "@/components/ui/select-box";
import { useAppearance } from "@/hooks/use-appearance";
import { useTheme } from "@/hooks/use-theme";
import { APPEARANCE_ACCENT_CSS, APPEARANCE_LABELS } from "@/lib/appearance";
import { PLAYER_APPEARANCES, type PlayerAppearance } from "@/types/ui";
import { Moon, Sun } from "lucide-react";

/** 空白布局：登录页（白名单，不做任何认证请求） */
export function BlankLayout() {
  const { theme, setTheme } = useTheme();
  const { appearance, setAppearance } = useAppearance();

  return (
    /* .admin-app：与后台主体共用同一套随风格变化的语义色板 */
    <div className="admin-app relative flex min-h-screen items-center justify-center p-4">
      {/* 右上角：界面风格 + 深浅主题（与后台头部一致） */}
      <div className="absolute right-4 top-4 flex items-center gap-2">
        <div className="flex items-center gap-2" title="界面风格（与播放页共用同一设置）">
          <span
            aria-hidden="true"
            className="h-3.5 w-3.5 shrink-0 rounded-full border border-border/60"
            style={{ backgroundColor: APPEARANCE_ACCENT_CSS }}
          />
          <SelectBox
            variant="sm"
            aria-label="界面风格"
            containerClassName="min-w-[5.5rem]"
            value={appearance}
            onChange={(e) => setAppearance(e.currentTarget.value as PlayerAppearance)}
          >
            {PLAYER_APPEARANCES.map((name) => (
              <option key={name} value={name}>
                {APPEARANCE_LABELS[name]}
              </option>
            ))}
          </SelectBox>
        </div>
        <Button
          variant="ghost"
          size="icon"
          title="切换主题"
          onClick={() => setTheme(theme === "dark" ? "light" : "dark")}
        >
          {theme === "dark" ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
        </Button>
      </div>
      <Outlet />
    </div>
  );
}
