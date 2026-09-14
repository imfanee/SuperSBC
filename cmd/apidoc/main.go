// Command apidoc renders the generated OpenAPI document as docs/API.md.
//
//	go run ./cmd/apidoc internal/httpapi/admin/openapi/swagger.json docs/API.md
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: apidoc <swagger.json> <API.md>")
		os.Exit(2)
	}
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	var doc struct {
		Info  struct{ Title, Version, Description string } `json:"info"`
		Paths map[string]map[string]struct {
			Summary    string   `json:"summary"`
			Tags       []string `json:"tags"`
			Parameters []struct {
				Name        string `json:"name"`
				In          string `json:"in"`
				Required    bool   `json:"required"`
				Description string `json:"description"`
			} `json:"parameters"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		panic(err)
	}
	byTag := map[string][]string{}
	for path, ops := range doc.Paths {
		for method, op := range ops {
			tag := "other"
			if len(op.Tags) > 0 {
				tag = op.Tags[0]
			}
			var params []string
			for _, p := range op.Parameters {
				if p.In == "path" {
					continue
				}
				req := ""
				if p.Required {
					req = " (required)"
				}
				params = append(params, fmt.Sprintf("`%s`%s", p.Name, req))
			}
			line := fmt.Sprintf("| `%s` | `%s` | %s |", strings.ToUpper(method), path, op.Summary)
			if len(params) > 0 {
				line = fmt.Sprintf("| `%s` | `%s` | %s<br>Query: %s |", strings.ToUpper(method), path, op.Summary, strings.Join(params, ", "))
			}
			byTag[tag] = append(byTag[tag], line)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", doc.Info.Title)
	b.WriteString("Generated from handler annotations by `make openapi` (swag v2, OpenAPI 3.1). The live document is served at `/api/docs` (Swagger UI) and `/api/docs/openapi.json`.\n\n")
	b.WriteString("Base path: `/api/v1`. Authentication: `POST /auth/login` sets `sbc_access` (JWT, 15 min), `sbc_refresh` (7 days, path `/api/v1/auth`) and `sbc_csrf` cookies; state changing requests must send `X-CSRF-Token` equal to the `sbc_csrf` cookie. API keys: `Authorization: Bearer sbc_...` (no CSRF). Roles: `viewer` read only, `operator` configuration and routing, `admin` everything including money and users.\n\n")
	b.WriteString("Money is always a decimal string (`\"0.020000\"`). Lists accept `page`, `per_page`, `sort` (prefix `-` for descending) and return `{items, total, page, per_page}`.\n\n")
	b.WriteString("## Internal call control API (port 8081, header `X-SBC-Secret`)\n\n")
	b.WriteString("| Method | Path | Purpose |\n|---|---|---|\n")
	b.WriteString("| `POST` | `/internal/v1/call/setup` | Steps 1 to 7 of the pipeline in one call: authorise, normalise, sell rate, balance, reserve, route, buy rates |\n")
	b.WriteString("| `POST` | `/internal/v1/call/authorize` | Step 1 only (probe, holds no slot) |\n")
	b.WriteString("| `POST` | `/internal/v1/call/rate` | Step 3 only |\n")
	b.WriteString("| `POST` | `/internal/v1/call/route` | Steps 6 and 7 only |\n")
	b.WriteString("| `POST` | `/internal/v1/call/attempt/begin` | Carrier capacity slot before a bridge attempt |\n")
	b.WriteString("| `POST` | `/internal/v1/call/attempt` | Report and classify one bridge attempt |\n")
	b.WriteString("| `POST` | `/internal/v1/call/release` | Release a reservation for a call that will not be dialled |\n")
	b.WriteString("| `POST` | `/internal/v1/cdr` | mod_json_cdr receiver (basic auth, password = secret) |\n\n")
	tags := make([]string, 0, len(byTag))
	for t := range byTag {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	for _, t := range tags {
		fmt.Fprintf(&b, "## %s\n\n| Method | Path | Summary |\n|---|---|---|\n", strings.ToUpper(t[:1])+t[1:])
		lines := byTag[t]
		sort.Strings(lines)
		for _, l := range lines {
			b.WriteString(l + "\n")
		}
		b.WriteString("\n")
	}
	if err := os.WriteFile(os.Args[2], []byte(b.String()), 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("wrote %s (%d paths)\n", os.Args[2], len(doc.Paths))
}
