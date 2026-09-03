import { api } from "./http";

export interface Task {
  name: string;
  enabled: boolean;
  group: string;
  cron: string;
  command: string;
  timeout: string;
  notes: string;
}

export interface TaskStatus {
  key: string;
  next_run?: string;
  ran?: boolean;
  success?: boolean;
  last_run?: string;
  last_duration?: string;
  last_message?: string;
}

export async function listTasks(): Promise<Task[]> {
  try {
    const data = await api.get<Task[]>("api/tasks/config");
    return Array.isArray(data) ? data : [];
  } catch {
    return [];
  }
}

export async function saveTasks(tasks: Task[]): Promise<{ message?: string }> {
  return api.post("api/tasks/config/save", tasks);
}

export async function listStatus(): Promise<TaskStatus[]> {
  try {
    const data = await api.get<TaskStatus[]>("api/tasks/status");
    return Array.isArray(data) ? data : [];
  } catch {
    return [];
  }
}

export async function runTaskNow(input: { command: string; timeout: string; key: string }): Promise<{
  success: boolean;
  duration?: string;
  output?: string;
  error?: string;
}> {
  return api.post("api/tasks/run", input);
}