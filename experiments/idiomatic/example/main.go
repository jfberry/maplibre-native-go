// Same program as ../../cforgo/example/main.go, written against
// the hand-written idiomatic binding.
//
// Compare line counts and the absence of cgo casts, sizeof
// hardcoding, [][]T slot dance, manual run_once pumping, and
// thread-affinity worry.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	maplibre "github.com/jfberry/maplibre-native-go/experiments/idiomatic"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rt, err := maplibre.NewRuntime()
	if err != nil {
		log.Fatal(err)
	}
	defer rt.Close()

	m, err := rt.NewMap(256, 256)
	if err != nil {
		log.Fatal(err)
	}
	defer m.Close()

	if err := m.SetStyleJSON(`{"version":8,"sources":{},"layers":[]}`); err != nil {
		log.Fatal(err)
	}
	if err := m.WaitForStyle(ctx); err != nil {
		log.Fatal(err)
	}
	fmt.Println("style loaded")

	sess, err := m.AttachTexture(256, 256)
	if err != nil {
		log.Fatal(err)
	}
	defer sess.Close()

	rgba, w, h, err := m.RenderImage(ctx, sess)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("rendered %dx%d (%d bytes)\n", w, h, len(rgba))
}
