package templates

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// catalogueHost answers the template filters that need the server (engine.Host)
// from the server catalogue, in one language - the render's. The same answer
// in-process and to the sandbox's host import, so a template says the same
// words wherever it ran.
type catalogueHost struct{ lang string }

func (h catalogueHost) Call(fn string, args []any) (string, error) {
	switch fn {
	case "t", "tpl":
		if len(args) < 1 {
			return "", fmt.Errorf("%s: no key", fn)
		}
		key, _ := args[0].(string)
		key = strings.TrimPrefix(strings.TrimSpace(key), srvtext.Prefix)
		full := srvtext.Prefix + key
		if key == "" || !srvtext.Has(full) {
			return "", fmt.Errorf("there is no sentence named %q", key)
		}
		vars := srvtext.Vars{}
		if len(args) > 1 {
			switch m := args[1].(type) {
			case map[string]string:
				for k, v := range m {
					vars[k] = v
				}
			case map[string]any:
				for k, v := range m {
					vars[k] = fmt.Sprint(v)
				}
			}
		}
		count, counted := countOf(vars)
		if fn == "tpl" {
			if counted {
				return srvtext.TemplateN(h.lang, full, count), nil
			}
			return srvtext.Template(h.lang, full), nil
		}
		if counted {
			return srvtext.Plural(h.lang, full, count, vars), nil
		}
		return srvtext.Text(h.lang, full, vars), nil
	case "bytes":
		if len(args) < 1 {
			return "", fmt.Errorf("bytes: no size")
		}
		var n int64
		switch v := args[0].(type) {
		case int64:
			n = v
		case int:
			n = int64(v)
		case float64:
			n = int64(v)
		default:
			n, _ = strconv.ParseInt(fmt.Sprint(v), 10, 64)
		}
		return srvtext.Bytes(h.lang, n), nil
	}
	return "", fmt.Errorf("no template helper named %q", fn)
}

func countOf(vars srvtext.Vars) (int, bool) {
	s, ok := vars["count"]
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		if f, ferr := strconv.ParseFloat(strings.TrimSpace(s), 64); ferr == nil {
			return int(f), true
		}
		return 0, false
	}
	return n, true
}
