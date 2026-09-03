interface CheckboxProps {
  checked: boolean;
  onChange: (v: boolean) => void;
}

export function Checkbox({ checked, onChange }: CheckboxProps) {
  return (
    <input
      type="checkbox"
      className="h-4 w-4 shrink-0 accent-[hsl(var(--primary))]"
      checked={checked}
      onChange={(e) => onChange(e.target.checked)}
    />
  );
}