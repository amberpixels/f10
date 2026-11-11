package facts

func VerifyFieldIn(fields []Field) Field {
	const name = "Verify"

	for _, f := range fields {
		if f.Name == name {
			return f
		}
	}

	return Field{Name: name, Origin: OriginOf(OriginAbsent)}
}
