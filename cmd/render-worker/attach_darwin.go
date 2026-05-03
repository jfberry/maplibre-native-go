//go:build darwin

package main

import maplibre "github.com/jfberry/maplibre-native-go"

func attachSession(m *maplibre.Map, w, h uint32, scale float64) (*maplibre.RenderSession, error) {
	return m.AttachMetalTexture(w, h, scale)
}
