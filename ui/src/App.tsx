import { RouterProvider } from "react-router-dom";
import { router } from "@/router";
import { useAppearance } from "@/hooks/use-appearance";
import { useTheme } from "@/hooks/use-theme";

export default function App() {
  useTheme();
  // 界面风格（6 套配色）：与播放页共用同一存储键与 CSS 类
  useAppearance();
  return <RouterProvider router={router} />;
}
