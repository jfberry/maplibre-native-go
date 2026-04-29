package main

import (
	"fmt"

	maplibre "github.com/jfberry/maplibre-native-go"
)

func main() {
	fmt.Printf("ABI v%d\n", maplibre.ABIVersion())
}
