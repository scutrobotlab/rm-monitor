package pathfmt

import (
	"bytes"
	"path"
	"regexp"
	"strings"
	"text/template"
)

type Data struct {
	Event      string
	Zone       string
	MatchName  string
	Order      int
	RedSchool  string
	RedName    string
	BlueSchool string
	BlueName   string
	RoundNo    int
	Role       string
}

func Render(matchNameTemplate, pathTemplate string, data Data) (string, error) {
	data, err := withMatchName(matchNameTemplate, data)
	if err != nil {
		return "", err
	}
	out, err := execute(pathTemplate, data)
	if err != nil {
		return "", err
	}
	return sanitizePath(out), nil
}

func RenderMatchDir(matchNameTemplate, matchDirTemplate string, data Data) (string, error) {
	data, err := withMatchName(matchNameTemplate, data)
	if err != nil {
		return "", err
	}
	out, err := execute(matchDirTemplate, data)
	if err != nil {
		return "", err
	}
	return sanitizePath(out), nil
}

func withMatchName(matchNameTemplate string, data Data) (Data, error) {
	name, err := execute(matchNameTemplate, data)
	if err != nil {
		return data, err
	}
	data.MatchName = name
	return data, nil
}

func execute(tpl string, data Data) (string, error) {
	t, err := template.New("path").Parse(tpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

var badSegmentChars = regexp.MustCompile(`[<>:"\\|?*\x00-\x1f]`)

func sanitizePath(p string) string {
	parts := strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' })
	for i, part := range parts {
		part = strings.TrimSpace(part)
		part = badSegmentChars.ReplaceAllString(part, "_")
		part = strings.Trim(part, ". ")
		if part == "" {
			part = "_"
		}
		parts[i] = part
	}
	return path.Join(parts...)
}
