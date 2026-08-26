package phpgo

func init() {
	builtins["print_r"] = func(e *Env, a []Value) (Value, error) {
		s := phpPrintR(a[0], 0)
		if len(a) >= 2 && a[1].ToBool() {
			return NewString(s), nil
		}
		e.writeOutput(s)
		return NewBool(true), nil
	}
	builtins["var_dump"] = func(e *Env, a []Value) (Value, error) {
		for _, v := range a {
			e.writeOutput(phpVarDump(v))
		}
		return NewNull(), nil
	}
}
