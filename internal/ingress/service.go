package ingress

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/helldriver666/prism/internal/types"
)

// pathToServiceType maps URL path prefixes to ServiceType.
var pathToServiceType = []struct {
	prefix      string
	serviceType types.ServiceType
}{
	{"/v1/chat/completions", types.ServiceChat},
	{"/v1/images/generations", types.ServiceImage},
	{"/v1/embeddings", types.ServiceEmbedding},
	{"/v1/audio/speech", types.ServiceTTS},
	{"/v1/audio/transcriptions", types.ServiceSTT},
	{"/v1/moderations", types.ServiceModeration},
}

// headerServiceType maps X-Prism-Service header values to ServiceType.
var headerServiceType = map[string]types.ServiceType{
	"chat":       types.ServiceChat,
	"image":      types.ServiceImage,
	"embedding":  types.ServiceEmbedding,
	"tts":        types.ServiceTTS,
	"stt":        types.ServiceSTT,
	"moderation": types.ServiceModeration,
	"3d_model":   types.Service3DModel,
}

// detectServiceType determines the ServiceType from the request.
// First checks URL path, then falls back to X-Prism-Service header.
func detectServiceType(r *http.Request) (types.ServiceType, error) {
	path := r.URL.Path

	for _, entry := range pathToServiceType {
		if strings.HasPrefix(path, entry.prefix) {
			return entry.serviceType, nil
		}
	}

	// Fallback to header.
	header := r.Header.Get("X-Prism-Service")
	if header != "" {
		if st, ok := headerServiceType[header]; ok {
			return st, nil
		}
		return "", fmt.Errorf("ingress: unknown service type in header: %q", header)
	}

	return "", fmt.Errorf("ingress: cannot determine service type from path %q", path)
}
