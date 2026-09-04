//go:build dev

package grpcconsole

import (
	"bytes"
	"embed"
	"html/template"
	"strings"

	"github.com/fullstorydev/grpcui/standalone"
)

//go:embed templates/*.gohtml
var templates embed.FS

// Guide is the guide content that will appear above grpcui's form.
//
// This package knows only how to render it; it does not know which methods the contract being explained has, or what must be attached.
// Whoever knows that is the adapter owning that contract — it is the one that supplies the Guide.
type Guide struct {
	Title string
	Intro string
	Steps []GuideStep
	Notes []string

	// DefaultMetadata prefills rows under the Request Metadata heading, in "name: value" form.
	// The value may be left empty — "authorization: Bearer ", for instance, so that only the copied value remains to be pasted.
	DefaultMetadata []string

	// Examples are entries that, when clicked, fill in the method, the data and the metadata all at once.
	// They are an explanation that performs itself, so the user need not retype the guide field by field.
	Examples []GuideExample
}

// GuideStep is one item in the sequence to follow — Title is optional.
// Without it you get a plain line, with no bold heading and no leading separator.
type GuideStep struct {
	Title string
	Body  string
}

type GuideExample struct {
	Name        string
	Description string

	// Service must be a fully qualified name such as "user.v1.AuthService", and Method a short name such as "Login".
	// These two are selected directly in the form's dropdowns; a typo means clicking does nothing.
	Service string
	Method  string

	// Data is the request message in a form that marshals to JSON.
	Data any

	// Metadata is in the same "name: value" form as DefaultMetadata, but bound to this example alone.
	Metadata []string
}

// handlerOptions turns a Guide into grpcui's options.
// An example that cannot be converted to JSON must not be skipped silently — but neither should one example bring down the whole console page.
func (g *Guide) handlerOptions() ([]standalone.HandlerOption, error) {
	if g == nil {
		return nil, nil
	}

	tmpl, err := indexTemplate(g)
	if err != nil {
		return nil, err
	}

	opts := []standalone.HandlerOption{
		standalone.WithIndexTemplate(tmpl),
		standalone.WithDefaultMetadata(g.DefaultMetadata),
	}

	if len(g.Examples) > 0 {
		examples := make([]standalone.Example, 0, len(g.Examples))
		for _, ex := range g.Examples {
			examples = append(examples, standalone.Example{
				Name:        ex.Name,
				Description: ex.Description,
				Service:     ex.Service,
				Method:      ex.Method,
				Request: standalone.ExampleRequest{
					Metadata: metadataPairs(ex.Metadata),
					Data:     ex.Data,
				},
			})
		}
		exOpt, err := standalone.WithExamples(examples...)
		if err != nil {
			return nil, err
		}
		opts = append(opts, exOpt)
	}
	return opts, nil
}

func metadataPairs(raw []string) []standalone.ExampleMetadataPair {
	pairs := make([]standalone.ExampleMetadataPair, 0, len(raw))
	for _, entry := range raw {
		name, value, found := strings.Cut(entry, ":")
		if !found {
			continue
		}
		pairs = append(pairs, standalone.ExampleMetadataPair{
			Name:  strings.TrimSpace(name),
			Value: strings.TrimLeft(value, " "),
		})
	}
	return pairs
}

// indexTemplate joins the guide panel onto grpcui's own page.
//
// grpcui calls this template with standalone.WebFormContainerTemplateData, which has no room for a Guide,
// and if the template references a field that does not exist, grpcui panics the whole process.
// So we render the panel first, then pass it in as a {{guide}} function that depends on none of grpcui's data.
func indexTemplate(g *Guide) (*template.Template, error) {
	panel, err := template.New("guide.gohtml").
		Funcs(template.FuncMap{"md": inlineCode}).
		ParseFS(templates, "templates/guide.gohtml")
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := panel.Execute(&buf, g); err != nil {
		return nil, err
	}
	// buf comes from executing an html/template, which has already escaped every value,
	// and the Guide content comes from code in this repo rather than from an end user.
	rendered := template.HTML(buf.String()) //nolint:gosec // G203: already escaped by the previous step

	return template.New("index.gohtml").
		Funcs(template.FuncMap{"guide": func() template.HTML { return rendered }}).
		ParseFS(templates, "templates/index.gohtml")
}

// inlineCode lets the guide content be written as plain prose with exactly one piece of meaningful markup:
// the backtick, which becomes <code> — so a guide author need not mix HTML tags into a sentence.
//
// Escape first and substitute second, so that text containing < or & stays just as safe.
func inlineCode(s string) template.HTML {
	parts := strings.Split(template.HTMLEscapeString(s), "`")

	var b strings.Builder
	for i, part := range parts {
		// The odd indices are the spans sitting between a pair of backticks.
		if i%2 == 1 {
			b.WriteString("<code>")
			b.WriteString(part)
			b.WriteString("</code>")
			continue
		}
		b.WriteString(part)
	}
	// b is assembled from the output of HTMLEscapeString plus the <code> tags written right here, and nothing else.
	return template.HTML(b.String()) //nolint:gosec // G203: everything originating from text was escaped above
}
