export interface Source {
  url: string;
  catchup?: string;
  catchupSource?: string;
  label?: string;
}

export interface Channel {
  id: string;
  name: string;
  logo?: string;
  groups: string[];
  /** 频道号（订阅列表中的序位，1 起）。用于界面展示，替代裸 id 短哈希。 */
  number?: number;
  tvgId?: string;
  tvgName?: string;
  sources: Source[];
}

export interface EPGProgram {
  id: string;
  title?: string;
  start: Date;
  end: Date;
}

export interface M3UMetadata {
  tvgUrl?: string;
  channels: Channel[];
  groups: string[];
}
