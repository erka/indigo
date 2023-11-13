package main

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/flosch/pongo2/v6"
	"github.com/labstack/echo/v4"
)

//go:embed templates/*
var TemplateFS embed.FS

type RendererLoader struct {
	prefix string
	fs     *embed.FS
}

func NewRendererLoader() pongo2.TemplateLoader {
	return &RendererLoader{
		fs: &TemplateFS,
	}
}
func (l *RendererLoader) Abs(base, name string) string {
	// There are a top-level templates and they have base as empty string.
	// Top-level template may include other template(s) and in this case their path
	// becomes relative to top-level. For example, templates/profile.html will be the `base` and
	// header.html will be the `name`.
	if base != "" {
		return filepath.Join(filepath.Dir(base), name)
	}
	return name
}

func (l *RendererLoader) Get(path string) (io.Reader, error) {
	b, err := l.fs.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading template %q failed: %w", path, err)
	}
	return bytes.NewReader(b), nil
}

type Renderer struct {
	TemplateSet *pongo2.TemplateSet
	Debug       bool
}

func NewRenderer(debug bool) *Renderer {
	return &Renderer{
		TemplateSet: pongo2.NewSet("web", NewRendererLoader()),
		Debug:       debug,
	}
}

func (r Renderer) Render(w io.Writer, name string, data interface{}, c echo.Context) error {
	var ctx pongo2.Context

	if data != nil {
		var ok bool
		ctx, ok = data.(pongo2.Context)
		if !ok {
			return errors.New("no pongo2.Context data was passed")
		}
	}

	var t *pongo2.Template
	var err error
	name = filepath.Join("templates", name)
	if r.Debug {
		t, err = pongo2.FromFile(name)
	} else {
		t, err = r.TemplateSet.FromFile(name)
	}

	if err != nil {
		return err
	}

	return t.ExecuteWriter(ctx, w)
}
