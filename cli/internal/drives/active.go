package drives

// Live is a drive neither cancelled nor done, and whether its driver runs:
// holds the drive's lease. A stopped drive still lists its tasks.
type Live struct {
	*Record

	Running bool
}

// Active is every live drive under root, newest first; an unreadable
// drives dir lists none.
func Active(root string) []Live {
	recs, err := List(root)
	if err != nil {
		return nil
	}

	var out []Live

	for _, r := range recs {
		if l, ok := live(root, r); ok {
			out = append(out, l)
		}
	}

	return out
}

// ActiveOne is drive n when it is live, false when it is done, cancelled
// or has no record.
func ActiveOne(root string, n int) (Live, bool) {
	r, err := Read(root, n)
	if err != nil {
		return Live{}, false
	}

	return live(root, r)
}

func live(root string, r *Record) (Live, bool) {
	if r.Cancelled != nil || r.Done() {
		return Live{}, false
	}

	_, running := Probe(root, DriveKey(r.Number))

	return Live{Record: r, Running: running}, true
}
