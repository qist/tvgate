import * as React from "react";

interface Props {
  children: React.ReactNode;
}
interface State {
  error: Error | null;
}

// 路由内容兜底：某个模块渲染抛错时只显示错误卡片，不影响侧栏与其他导航。
export class ErrorBoundary extends React.Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error) {
    console.error("[ErrorBoundary]", error);
  }

  render() {
    if (this.state.error) {
      return (
        <div className="space-y-2 rounded-lg border border-destructive/40 bg-destructive/10 p-4">
          <p className="text-sm font-semibold text-destructive">该模块渲染出错</p>
          <pre className="whitespace-pre-wrap break-all rounded bg-background/60 p-2 font-mono text-xs text-muted-foreground">
            {this.state.error.message}
          </pre>
          <button
            onClick={() => this.setState({ error: null })}
            className="rounded border border-input bg-background px-3 py-1 text-sm hover:bg-accent"
          >
            重试
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}