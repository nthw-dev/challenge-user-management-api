//go:build dev

package grpcconsole

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"github.com/fullstorydev/grpcui/standalone"
	"github.com/stretchr/testify/require"
)

func TestNew_Target(t *testing.T) {
	t.Parallel()

	t.Run("the console must point at a dialable address, not a listen address", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, "localhost:9090", New(":9090", nil).Target())
	})
}

func TestGuide_HandlerOptions(t *testing.T) {
	t.Parallel()

	t.Run("with no guide we must get grpcui's default page, not an error", func(t *testing.T) {
		t.Parallel()

		opts, err := New(":9090", nil).guide.handlerOptions()

		require.NoError(t, err)
		require.Empty(t, opts)
	})

	t.Run("with a guide we must get the template, the initial metadata and the examples", func(t *testing.T) {
		t.Parallel()

		opts, err := New(":9090", nil, WithGuide(sampleGuide())).guide.handlerOptions()

		require.NoError(t, err)
		require.Len(t, opts, 3)
	})
}

// grpcui calls the template with its own data and panics the whole process if the template references something absent,
// so this test calls it with the very same data grpcui really uses.
func TestGuide_IndexTemplate(t *testing.T) {
	t.Parallel()

	g := sampleGuide()
	tmpl, err := indexTemplate(&g)
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, tmpl.Execute(&buf, standalone.WebFormContainerTemplateData{
		Target:          "localhost:9090",
		WebFormContents: template.HTML(`<form id="grpc-form"></form>`),
	}))
	got := buf.String()

	require.Contains(t, got, "How to attach metadata")
	require.Contains(t, got, "localhost:9090")
	// grpcui's form must still be intact; the guide panel is an addition, not a replacement.
	require.Contains(t, got, `<form id="grpc-form"></form>`)
	require.Contains(t, got, "Get a token")
	require.Contains(t, got, "the name must be lowercase")
}

func TestInlineCode(t *testing.T) {
	t.Parallel()

	t.Run("a backtick becomes code", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, template.HTML("attach <code>authorization</code> as well"),
			inlineCode("attach `authorization` as well"))
	})

	t.Run("HTML characters must be escaped both inside and outside backticks", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, template.HTML("<code>Bearer &lt;token&gt;</code> &amp; see &lt;b&gt;"),
			inlineCode("`Bearer <token>` & see <b>"))
	})

	t.Run("an unpaired backtick must not leave a tag dangling", func(t *testing.T) {
		t.Parallel()
		got := string(inlineCode("opened `and forgot to close"))
		require.Equal(t, strings.Count(got, "<code>"), strings.Count(got, "</code>"))
	})
}

func TestMetadataPairs(t *testing.T) {
	t.Parallel()

	got := metadataPairs([]string{
		"authorization: Bearer ",
		"x-request-id: 01J8",
		"no separator at all",
	})

	// The space after Bearer must survive, otherwise the user pastes the token straight onto it and gets one run-together word.
	require.Equal(t, []standalone.ExampleMetadataPair{
		{Name: "authorization", Value: "Bearer "},
		{Name: "x-request-id", Value: "01J8"},
	}, got)
}

func sampleGuide() Guide {
	return Guide{
		Title: "How to attach metadata",
		Intro: "you must attach `authorization`",
		Steps: []GuideStep{{Title: "Get a token", Body: "call `Login` first"}},
		Notes: []string{"the name must be lowercase"},

		DefaultMetadata: []string{"authorization: Bearer "},
		Examples: []GuideExample{{
			Name:     "Login",
			Service:  "user.v1.AuthService",
			Method:   "Login",
			Data:     map[string]string{"email": "a@b.co"},
			Metadata: []string{"authorization: Bearer "},
		}},
	}
}
