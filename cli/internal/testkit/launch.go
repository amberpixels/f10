package testkit

const (
	BoxRule  = "────────────────────────────────────────"
	SentText = "/f10:ship GH-1 resume at pr (entry 4) --driven"
)

func ScreenWith(box string) string {
	return "⏺ earlier output\n❯ /f10:plan GH-1 --driven\n\n" + BoxRule + "\n" + box + "\n" + BoxRule + "\n  status line\n"
}
