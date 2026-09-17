import { useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { useNavigate } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { login } from "@/api/auth";

const schema = z.object({
  username: z.string().min(1, "请输入用户名"),
  password: z.string().min(1, "请输入密码"),
});

type Form = z.infer<typeof schema>;

/** 登录页（白名单，无品牌特征，不做任何认证请求，由 AppShell 守卫决定进出） */
export function Login() {
  const navigate = useNavigate();
  const [error, setError] = useState("");
  const { register, handleSubmit } = useForm<Form>({ resolver: zodResolver(schema) });

  const onSubmit = async (v: Form) => {
    setError("");
    try {
      await login(v.username, v.password);
      navigate("/", { replace: true });
    } catch (e) {
      setError((e as Error).message || "登录失败");
    }
  };

  return (
    <div className="w-full max-w-sm rounded-[var(--radius-lg)] border border-border/60 bg-card/80 p-7 shadow-[0_1px_2px_rgba(2,6,23,0.05),0_28px_70px_-38px_rgba(2,6,23,0.65)] backdrop-blur-xl dark:bg-card/70">
      {/* 顶部一条风格色细线（随「界面风格」变色，不做品牌元素） */}
      <div className="mx-auto mb-5 h-1 w-12 rounded-full bg-[linear-gradient(90deg,rgb(var(--pg-rgb)),rgb(var(--pg-rgb-2)))] shadow-[0_0_14px_rgba(var(--pg-rgb),0.55)]" />
      <h1 className="mb-1 text-center text-lg font-semibold tracking-[-0.01em] text-card-foreground">登录</h1>
      <p className="mb-6 text-center text-xs text-muted-foreground">请输入账号与密码</p>
      <form onSubmit={handleSubmit(onSubmit)} className="space-y-4">
        <div className="space-y-1.5">
          <Label htmlFor="username">用户名</Label>
          <Input id="username" placeholder="请输入用户名" autoFocus autoComplete="username" {...register("username")} />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="password">密码</Label>
          <Input id="password" type="password" placeholder="请输入密码" autoComplete="current-password" {...register("password")} />
        </div>
        {error && (
          <p className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
            {error}
          </p>
        )}
        <Button type="submit" size="lg" className="w-full">
          登录
        </Button>
      </form>
    </div>
  );
}