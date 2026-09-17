/**
 * 播放器设置下拉。
 * 主题 / 外观 / 面板透明度 / 画中画模式 用下拉框；无缝换台、自动去隔行、画质增强 用开关。
 * 面板外点击或 Esc 关闭（Esc 后焦点回到触发按钮）。
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

const SETTING_LABEL_CLASS = "block px-0.5 font-medium text-slate-500 text-xs leading-4 dark:text-violet-50/55";
const SETTING_SWITCH_CLASS = "min-h-6 gap-3 px-0.5";
const SETTING_SWITCH_LABEL_CLASS = "flex-1 font-medium text-slate-600 text-xs leading-4 dark:text-violet-50/65";
const SETTING_SWITCH_CONTROL_CLASS =
  "border-violet-900/10 bg-slate-200/75 shadow-inner data-[state=checked]:border-violet-300/35 data-[state=checked]:bg-violet-500 data-[state=checked]:shadow-[0_0_16px_rgba(var(--pg-rgb),0.24)] dark:border-violet-100/10 dark:bg-slate-800/80";
const SETTINGS_POPOVER_ID = "player-settings-popover";

interface SettingSelectProps<Value extends string> {
  id: string;
  label: string;
  value: Value;
  options: readonly { value: Value; label: string }[];
  onChange: (value: Value) => void;
}

function SettingSelect<Value extends string>({ id, label, value, options, onChange }: SettingSelectProps<Value>) {
  return (
    <div className="grid grid-cols-[5.75rem_minmax(0,1fr)] items-center gap-2">
      <label htmlFor={id} className={SETTING_LABEL_CLASS}>
        {label}
      </label>
      <SelectBox
        id={id}
        value={value}
        onChange={(event) => onChange(event.target.value as Value)}
        variant="sm"
        containerClassName="w-full min-w-0"
        aria-label={label}
      >
        {options.map((option) => (
          <option key={option.value} value={option.value}>
            {option.label}
          </option>
        ))}
      </SelectBox>
    </div>
  );
}

function SettingsDropdownComponent({
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
  const [isOpen, setIsOpen] = useState(false);
  const dropdownRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);

  const themeOptions = THEME_MODES.map((value) => ({ value, label: t(THEME_LABEL_KEYS[value]) }));
  const appearanceOptions = PLAYER_APPEARANCES.map((value) => ({ value, label: t(PLAYER_APPEARANCE_LABEL_KEYS[value]) }));
  const panelAlphaOptions = PLAYER_PANEL_ALPHAS.map((value) => ({ value, label: t(PLAYER_PANEL_ALPHA_LABEL_KEYS[value]) }));
  const pipOptions = PICTURE_IN_PICTURE_MODES.map((value) => ({ value, label: t(PICTURE_IN_PICTURE_MODE_LABEL_KEYS[value]) }));

  useEffect(() => {
    if (!isOpen) return;
    const onPointerDown = (event: PointerEvent) => {
      if (!dropdownRef.current?.contains(event.target as Node)) setIsOpen(false);
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setIsOpen(false);
        triggerRef.current?.focus();
      }
    };
    document.addEventListener("pointerdown", onPointerDown);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("pointerdown", onPointerDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [isOpen]);

  return (
    <div ref={dropdownRef} className="relative size-8 md:size-9">
      <button
        ref={triggerRef}
        type="button"
        onClick={() => setIsOpen((open) => !open)}
        aria-haspopup="dialog"
        aria-expanded={isOpen}
        aria-controls={SETTINGS_POPOVER_ID}
        className="player-performance-effect player-performance-motion flex size-8 cursor-pointer items-center justify-center rounded-xl border border-transparent p-0 text-slate-500 transition-[color,background-color,border-color,box-shadow,transform] motion-reduce:transition-none hover:border-violet-400/20 hover:bg-violet-400/10 hover:text-violet-700 hover:shadow-[0_0_18px_rgba(var(--pg-rgb),0.1)] motion-safe:active:scale-95 dark:text-slate-400 dark:hover:text-violet-200 md:size-9"
        title={t("settings")}
      >
        <Settings className="h-5 w-5" />
      </button>

      {isOpen && (
        <div
          id={SETTINGS_POPOVER_ID}
          role="dialog"
          aria-label={t("settings")}
          className="player-performance-panel-background player-performance-effect player-performance-gradient absolute top-full right-0 z-50 mt-1 max-h-[calc(100vh-4rem)] w-60 max-w-[calc(100vw-1rem)] overflow-y-auto rounded-2xl border border-violet-900/12 bg-[linear-gradient(145deg,rgba(255,255,255,0.9),rgba(241,245,249,0.82))] p-0 shadow-[0_20px_55px_rgba(2,6,23,0.28),inset_0_1px_0_rgba(255,255,255,0.82)] backdrop-blur-2xl dark:border-violet-100/15 dark:bg-[linear-gradient(145deg,rgba(2,6,23,0.94),rgba(10,16,32,0.9))] dark:shadow-[0_22px_60px_rgba(2,6,16,0.62),inset_0_1px_0_rgba(255,255,255,0.08)]"
        >
          <div className="space-y-2.5 p-2.5">
            <SettingSelect id="player-settings-theme" label={t("theme")} value={theme} options={themeOptions} onChange={onThemeChange} />
            <SettingSelect
              id="player-settings-appearance"
              label={t("appearance")}
              value={appearance}
              options={appearanceOptions}
              onChange={onAppearanceChange}
            />
            <SettingSelect
              id="player-settings-panel-alpha"
              label={t("panelAlpha")}
              value={panelAlpha}
              options={panelAlphaOptions}
              onChange={onPanelAlphaChange}
            />
            {showPictureInPictureMode && (
              <SettingSelect
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
                className={SETTING_SWITCH_CLASS}
                labelClassName={SETTING_SWITCH_LABEL_CLASS}
                switchClassName={SETTING_SWITCH_CONTROL_CLASS}
              />
            )}

            {/* 软解音频声道模式（MP2/AC-3）：mono 把左右合成单声道，
                只出一路声道的设备（如手机）也能听到两边内容。 */}
            <div className="space-y-2.5 border-violet-900/10 border-t pt-2.5 dark:border-violet-100/10">
              <SettingSelect
                id="player-settings-audio-channel-mode"
                label={t("audioChannelMode")}
                value={audioChannelMode}
                options={[
                  { value: "stereo", label: t("audioChannelModeStereo") },
                  { value: "mono", label: t("audioChannelModeMono") },
                ]}
                onChange={onAudioChannelModeChange}
              />
            </div>

            {showVideoProcessing && (
              <div className="space-y-2.5 border-violet-900/10 border-t pt-2.5 dark:border-violet-100/10">
                <div className="px-0.5">
                  <span className="block whitespace-nowrap text-[11px] text-slate-400 leading-4 dark:text-violet-50/35">
                    {t("resolutionLimitHint")}
                  </span>
                </div>
                <LabeledSwitch
                  label={t("deinterlace")}
                  checked={autoDeinterlace}
                  onCheckedChange={onAutoDeinterlaceChange}
                  className={SETTING_SWITCH_CLASS}
                  labelClassName={SETTING_SWITCH_LABEL_CLASS}
                  switchClassName={SETTING_SWITCH_CONTROL_CLASS}
                />
                <LabeledSwitch
                  label={t("pictureEnhancement")}
                  checked={pictureEnhancement}
                  onCheckedChange={onPictureEnhancementChange}
                  className={SETTING_SWITCH_CLASS}
                  labelClassName={SETTING_SWITCH_LABEL_CLASS}
                  switchClassName={SETTING_SWITCH_CONTROL_CLASS}
                />
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

export const SettingsDropdown = memo(SettingsDropdownComponent);
