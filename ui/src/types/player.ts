/** 频道可播放源。timeshift 标记时移回看类型；timeshiftTemplate 为自定义回看地址模板。 */
export interface Source {
  url: string;
  /**
   * 时移能力标记：值为服务端约定的能力代号（如 "server"）。
   * 与 timeshiftTemplate 必须同时存在才视为"该源可回看"——只看其一会误报能力。
   */
  timeshift?: string;
  /** 自定义时移地址模板；占位符由播放侧在发起回看时替换。 */
  timeshiftTemplate?: string;
  /** 源别名（换源菜单展示用），缺省时界面回退到"线路 N"序号文案。 */
  alias?: string;
}

export interface Channel {
  id: string;
  name: string;
  groups: string[];
  sources: Source[];
  logo?: string;
  /** 频道号（订阅列表中的序位，1 起）。用于界面展示，替代裸 id 短哈希。 */
  number?: number;
  /**
   * XMLTV 节目单里该频道的 id / 显示名。二者都是 EPG 关联键的候选，
   * 实际回退顺序（epgId → epgName → name）收敛在 lib/epg-parser，两侧必须一致。
   */
  epgId?: string;
  epgName?: string;
}

/** 一档节目：beginsAt 含、endsAt 不含（左闭右开），换台临界时刻归属后一档。 */
export interface EPGProgram {
  id: string;
  title?: string;
  beginsAt: Date;
  endsAt: Date;
}

/** M3U 订阅解析产物；epgUrl 为 XMLTV 节目单地址。 */
export interface M3UMetadata {
  epgUrl?: string;
  channels: Channel[];
  groups: string[];
}
