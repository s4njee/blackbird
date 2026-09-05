package storage

import "strings"

func parseMounts(data string) []mountPoint {
	out := []mountPoint{}
	unescape := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		out = append(out, mountPoint{fields[0], unescape.Replace(fields[4])})
	}
	return out
}

func mountFor(path string, points []mountPoint) string {
	best, id := "", ""
	for _, m := range points {
		if !inside(m.path, path) {
			continue
		}
		if len(m.path) > len(best) {
			best, id = m.path, m.id
		} else if m.path == best && m.id != id {
			id = ""
		}
	}
	return id
}
