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

/** 登录页（白名单，不做任何认证请求，由 AppShell 守卫决定进出） */
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
    <div className="w-full max-w-sm rounded-[var(--radius-lg)] border bg-card p-6 shadow-sm">
      <h1 className="mb-1 text-xl font-semibold text-card-foreground">TVGate 登录</h1>
      <p className="mb-6 text-sm text-muted-foreground">登录管理后台</p>
      <form onSubmit={handleSubmit(onSubmit)} className="space-y-4">
        <div className="space-y-1.5">
          <Label htmlFor="username">用户名</Label>
          <Input id="username" placeholder="用户名" autoFocus {...register("username")} />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="password">密码</Label>
          <Input id="password" type="password" placeholder="密码" {...register("password")} />
        </div>
        {error && <p className="text-sm text-destructive">{error}</p>}
        <Button type="submit" className="w-full">
          登录
        </Button>
      </form>
    </div>
  );
}