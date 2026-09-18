/**
 * 播放器设置入口：齿轮按钮 + 弹出式设置面板。
 *
 * 交互约定：
 * - 面板打开期间在 document 上挂 pointerdown / keydown 监听：点击面板外区域关闭；
 *   Esc 关闭并把焦点还给齿轮按钮，保证键盘用户的焦点不落到 body 上丢失。
 * - 单选型设置（主题/外观/面板透明度/画中画模式/音频声道）统一走 SettingDropdownRow
 *   渲染成"左标签 + 右下拉框"的两列栅格；布尔型设置走 LabeledSwitch。
 *   选项文案由各模式数组 + 对应 i18n key 表派生，新增档位只需改 types/ui。
 */
import { Settings } from "lucide-react";
import { memo, useEffect, useRef, useState } from "react";
import { usePlayerTranslation } from "../../hooks/use-player-translation";
import type { Locale } from "../../lib/locale";
import {
  PICTURE_IN_PICTURE_MODE_LABEL_KEYS,
  PICTURE_IN_PICTURE_MODES,
  type PictureInPictureMode,
  PLAYER_APPEARANCE_LABEL_KEYS,
  PLAYER_APPEARANCES,
  type PlayerAppearance,
  PLAYER_PANEL_ALPHA_LABEL_KEYS,
  PLAYER_PANEL_ALPHAS,
  type PlayerPanelAlpha,
  THEME_LABEL_KEYS,
  THEME_MODES,
  type ThemeMode,
} from "../../types/ui";
import { LabeledSwitch } from "../ui/labeled-switch";
import { SelectBox } from "../ui/select-box";

interface SettingsDropdownProps {
  locale: Locale;
  theme: ThemeMode;
  onThemeChange: (theme: ThemeMode) => void;
  appearance: PlayerAppearance;
  onAppearanceChange: (appearance: PlayerAppearance) => void;
  panelAlpha: PlayerPanelAlpha;
  onPanelAlphaChange: (alpha: PlayerPanelAlpha) => void;
  pictureInPictureMode: PictureInPictureMode;
  onPictureInPictureModeChange: (mode: PictureInPictureMode) => void;
  seamlessSwitch: boolean;
  onSeamlessSwitchChange: (enabled: boolean) => void;
  autoDeinterlace: boolean;
  onAutoDeinterlaceChange: (enabled: boolean) => void;
  pictureEnhancement: boolean;
  onPictureEnhancementChange: (enabled: boolean) => void;
  /** 软解音频（MP2/AC-3）声道输出模式：mono = 左右合成单声道。 */
  audioChannelMode: "stereo" | "mono";
  onAudioChannelModeChange: (mode: "stereo" | "mono") => void;
  showSeamlessSwitch?: boolean;
  showPictureInPictureMode?: boolean;
  showVideoProcessing?: boolean;
}

const ROW_LABEL_CLASS = "block px-0.5 font-medium text-slate-500 text-xs leading-4 dark:text-violet-50/55";
const ROW_SWITCH_CLASS = "min-h-6 gap-3 px-0.5";
const ROW_SWITCH_LABEL_CLASS = "flex-1 font-medium text-slate-600 text-xs leading-4 dark:text-violet-50/65";
const ROW_SWITCH_TRACK_CLASS =
  "border-violet-900/10 bg-slate-200/75 shadow-inner data-[state=checked]:border-violet-300/35 data-[state=checked]:bg-violet-500 data-[state=checked]:shadow-[0_0_16px_rgba(var(--pg-rgb),0.24)] dark:border-violet-100/10 dark:bg-slate-800/80";
/** 分组之间的细分隔线：同时承担"间距"职责（space-y + pt 组合）。 */
const ROW_GROUP_DIVIDER_CLASS = "space-y-2.5 border-violet-900/10 border-t pt-2.5 dark:border-violet-100/10";
const POPOVER_ELEMENT_ID = "player-settings-popover";

/**
 * 把"档位数组 + 档位 → i18n key 映射"编译成下拉框选项表。
 * 泛型约束保证档位与文案 key 一一对应，漏配 key 会在编译期报错。
 */
function buildLabeledOptions<Mode extends string, LabelKey extends string>(
  modes: readonly Mode[],
  labelKeys: Record<Mode, LabelKey>,
  translate: (key: LabelKey) => string,
) {
  return modes.map((mode) => ({ value: mode, label: translate(labelKeys[mode]) }));
}

interface SettingRowProps<Value extends string> {
  id: string;
  label: string;
  value: Value;
  options: readonly { value: Value; label: string }[];
  onChange: (value: Value) => void;
}

/** 单行单选设置：左列固定宽度的 label（点击可聚焦下拉框），右列自适应的 SelectBox。 */
function SettingDropdownRow<Value extends string>({ id, label, value, options, onChange }: SettingRowProps<Value>) {
  // 单个 <option> 的渲染收口成独立回调；map 处只保留装配，字段解构重命名以免与行级 props 同名混淆。
  const renderOption = ({ value: optionValue, label: optionLabel }: { value: Value; label: string }) => (
    <option key={optionValue} value={optionValue}>
      {optionLabel}
    </option>
  );

  return (
    <div className="grid grid-cols-[5.75rem_minmax(0,1fr)] items-center gap-2">
      <label htmlFor={id} className={ROW_LABEL_CLASS}>
        {label}
      </label>
      <SelectBox
        id={id}
        value={value}
        containerClassName="w-full min-w-0"
        onChange={(event) => onChange(event.target.value as Value)}
        aria-label={label}
        variant="sm"
      >
        {options.map(renderOption)}
      </SelectBox>
    </div>
  );
}

/** 声音输出组：软解音频（MP2/AC-3）声道模式，单独成组并以分隔线与上方设置区隔。 */
function AudioChannelModeGroup(props: {
  rowLabel: string;
  stereoLabel: string;
  monoLabel: string;
  mode: "stereo" | "mono";
  onModeChange: (mode: "stereo" | "mono") => void;
}) {
  const { rowLabel, stereoLabel, monoLabel, mode, onModeChange } = props;
  return (
    <div className={ROW_GROUP_DIVIDER_CLASS}>
      <SettingDropdownRow
        id="player-settings-audio-channel-mode"
        label={rowLabel}
        value={mode}
        options={[
          { value: "stereo", label: stereoLabel },
          { value: "mono", label: monoLabel },
        ]}
        onChange={onModeChange}
      />
    </div>
  );
}

/** 视频处理组：清晰度上限提示 + 去隔行 / 画面增强两个开关。 */
function VideoProcessingGroup(props: {
  hint: string;
  deinterlaceLabel: string;
  pictureEnhancementLabel: string;
  autoDeinterlace: boolean;
  onAutoDeinterlaceChange: (enabled: boolean) => void;
  pictureEnhancement: boolean;
  onPictureEnhancementChange: (enabled: boolean) => void;
}) {
  const {
    hint,
    deinterlaceLabel,
    pictureEnhancementLabel,
    autoDeinterlace,
    onAutoDeinterlaceChange,
    pictureEnhancement,
    onPictureEnhancementChange,
  } = props;
  return (
    <div className={ROW_GROUP_DIVIDER_CLASS}>
      {/* 提示文案放最前：先解释下面两个开关共同的上限来源，再看开关。 */}
      <div className="px-0.5">
        <span className="block whitespace-nowrap text-[11px] text-slate-400 leading-4 dark:text-violet-50/35">{hint}</span>
      </div>
      <LabeledSwitch
        checked={autoDeinterlace}
        onCheckedChange={onAutoDeinterlaceChange}
        label={deinterlaceLabel}
        className={ROW_SWITCH_CLASS}
        labelClassName={ROW_SWITCH_LABEL_CLASS}
        switchClassName={ROW_SWITCH_TRACK_CLASS}
      />
      <LabeledSwitch
        checked={pictureEnhancement}
        onCheckedChange={onPictureEnhancementChange}
        label={pictureEnhancementLabel}
        className={ROW_SWITCH_CLASS}
        labelClassName={ROW_SWITCH_LABEL_CLASS}
        switchClassName={ROW_SWITCH_TRACK_CLASS}
      />
    </div>
  );
}

function SettingsDropdownWidget({
  locale,
  theme,
  onThemeChange,
  appearance,
  onAppearanceChange,
  panelAlpha,
  onPanelAlphaChange,
  pictureInPictureMode,
  onPictureInPictureModeChange,
  seamlessSwitch,
  onSeamlessSwitchChange,
  autoDeinterlace,
  onAutoDeinterlaceChange,
  pictureEnhancement,
  onPictureEnhancementChange,
  audioChannelMode,
  onAudioChannelModeChange,
  showSeamlessSwitch = true,
  showPictureInPictureMode = false,
  showVideoProcessing = true,
}: SettingsDropdownProps) {
  const t = usePlayerTranslation(locale);
  const [isPanelOpen, setIsPanelOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);
  const gearButtonRef = useRef<HTMLButtonElement>(null);

  // 选项表每轮渲染重建：文案随 locale 即时切换，无需额外的缓存失效逻辑。
  const themeOptions = buildLabeledOptions(THEME_MODES, THEME_LABEL_KEYS, t);
  const appearanceOptions = buildLabeledOptions(PLAYER_APPEARANCES, PLAYER_APPEARANCE_LABEL_KEYS, t);
  const panelAlphaOptions = buildLabeledOptions(PLAYER_PANEL_ALPHAS, PLAYER_PANEL_ALPHA_LABEL_KEYS, t);
  const pipOptions = buildLabeledOptions(PICTURE_IN_PICTURE_MODES, PICTURE_IN_PICTURE_MODE_LABEL_KEYS, t);

  // 关闭行为只在面板打开期间挂监听：pointerdown 命中面板外即关闭；
  // Esc 关闭后焦点归还触发按钮（无障碍要求）。
  useEffect(() => {
    if (!isPanelOpen) return;
    const handleGlobalPointerDown = (event: PointerEvent) => {
      if (!containerRef.current?.contains(event.target as Node)) setIsPanelOpen(false);
    };
    const handleGlobalKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setIsPanelOpen(false);
        gearButtonRef.current?.focus();
      }
    };
    document.addEventListener("pointerdown", handleGlobalPointerDown);
    document.addEventListener("keydown", handleGlobalKeyDown);
    return () => {
      document.removeEventListener("pointerdown", handleGlobalPointerDown);
      document.removeEventListener("keydown", handleGlobalKeyDown);
    };
  }, [isPanelOpen]);

  return (
    <div ref={containerRef} className="relative size-8 md:size-9">
      <button
        ref={gearButtonRef}
        type="button"
        onClick={() => setIsPanelOpen((open) => !open)}
        aria-haspopup="dialog"
        aria-expanded={isPanelOpen}
        aria-controls={POPOVER_ELEMENT_ID}
        className="player-performance-effect player-performance-motion flex size-8 cursor-pointer items-center justify-center rounded-xl border border-transparent p-0 text-slate-500 transition-[color,background-color,border-color,box-shadow,transform] motion-reduce:transition-none hover:border-violet-400/20 hover:bg-violet-400/10 hover:text-violet-700 hover:shadow-[0_0_18px_rgba(var(--pg-rgb),0.1)] motion-safe:active:scale-95 dark:text-slate-400 dark:hover:text-violet-200 md:size-9"
        title={t("settings")}
      >
        <Settings className="h-5 w-5" />
      </button>

      {isPanelOpen && (
        <div
          id={POPOVER_ELEMENT_ID}
          role="dialog"
          aria-label={t("settings")}
          className="player-performance-panel-background player-performance-effect player-performance-gradient absolute top-full right-0 z-50 mt-1 max-h-[calc(100vh-4rem)] w-60 max-w-[calc(100vw-1rem)] overflow-y-auto rounded-2xl border border-violet-900/12 bg-[linear-gradient(145deg,rgba(255,255,255,0.9),rgba(241,245,249,0.82))] p-0 shadow-[0_20px_55px_rgba(2,6,23,0.28),inset_0_1px_0_rgba(255,255,255,0.82)] backdrop-blur-2xl dark:border-violet-100/15 dark:bg-[linear-gradient(145deg,rgba(2,6,23,0.94),rgba(10,16,32,0.9))] dark:shadow-[0_22px_60px_rgba(2,6,16,0.62),inset_0_1px_0_rgba(255,255,255,0.08)]"
        >
          <div className="space-y-2.5 p-2.5">
            <SettingDropdownRow id="player-settings-theme" label={t("theme")} value={theme} options={themeOptions} onChange={onThemeChange} />
            <SettingDropdownRow
              id="player-settings-appearance"
              label={t("appearance")}
              value={appearance}
              options={appearanceOptions}
              onChange={onAppearanceChange}
            />
            <SettingDropdownRow
              id="player-settings-panel-alpha"
              label={t("panelAlpha")}
              value={panelAlpha}
              options={panelAlphaOptions}
              onChange={onPanelAlphaChange}
            />
            {showPictureInPictureMode && (
              <SettingDropdownRow
                id="player-settings-picture-in-picture-mode"
                label={t("pictureInPictureMode")}
                value={pictureInPictureMode}
                options={pipOptions}
                onChange={onPictureInPictureModeChange}
              />
            )}

            {showSeamlessSwitch && (
              <LabeledSwitch
                label={t("seamlessSwitch")}
                checked={seamlessSwitch}
                onCheckedChange={onSeamlessSwitchChange}
                className={ROW_SWITCH_CLASS}
                labelClassName={ROW_SWITCH_LABEL_CLASS}
                switchClassName={ROW_SWITCH_TRACK_CLASS}
              />
            )}

            {/* 软解音频声道模式（MP2/AC-3）单独成组：mono 把左右两路合成单声道，
                让只出一路声道的设备（如手机单扬声器）也能同时听到两侧内容。 */}
            <AudioChannelModeGroup
              rowLabel={t("audioChannelMode")}
              stereoLabel={t("audioChannelModeStereo")}
              monoLabel={t("audioChannelModeMono")}
              mode={audioChannelMode}
              onModeChange={onAudioChannelModeChange}
            />

            {showVideoProcessing && (
              <VideoProcessingGroup
                hint={t("resolutionLimitHint")}
                deinterlaceLabel={t("deinterlace")}
                pictureEnhancementLabel={t("pictureEnhancement")}
                autoDeinterlace={autoDeinterlace}
                onAutoDeinterlaceChange={onAutoDeinterlaceChange}
                pictureEnhancement={pictureEnhancement}
                onPictureEnhancementChange={onPictureEnhancementChange}
              />
            )}
          </div>
        </div>
      )}
    </div>
  );
}

export const SettingsDropdown = memo(SettingsDropdownWidget);
