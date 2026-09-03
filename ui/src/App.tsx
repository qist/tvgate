import { RouterProvider } from "react-router-dom";
import { router } from "@/router";
import { useTheme } from "@/hooks/use-theme";

export default function App() {
  useTheme();
  return <RouterProvider router={router} />;
}