//go:build ignore

// Command gen_land writes land.txt, the land bitmap the Location screen's
// map is drawn from, out of Natural Earth's 1:110m land polygons, which are
// in the public domain.
//
//	go generate ./internal/wizard
//
// It downloads the polygons, or reads the GeoJSON file given as its
// argument. Each output line is one degree of latitude, from 75°N down to
// 56°S, as 90 hex digits: 360 one-degree cells from 180°W eastward, high
// bit first. A cell is land when at least half of its 4×4 sample points
// are.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
)

const (
	source  = "https://raw.githubusercontent.com/nvkelso/natural-earth-vector/master/geojson/ne_110m_land.geojson"
	output  = "land.txt"
	north   = 75
	south   = -56
	columns = 360
	samples = 4
	digits  = "0123456789abcdef"
)

type geometry struct {
	Type        string          `json:"type"`
	Coordinates json.RawMessage `json:"coordinates"`
}

type collection struct {
	Features []struct {
		Geometry geometry `json:"geometry"`
	} `json:"features"`
}

// point is a longitude and a latitude, as GeoJSON orders them.
type point [2]float64

func main() {
	data, err := read(os.Args[1:])
	must(err)

	var land collection
	must(json.Unmarshal(data, &land))

	var all [][]point
	for _, feature := range land.Features {
		rings, err := rings(feature.Geometry)
		must(err)

		all = append(all, rings...)
	}

	must(os.WriteFile(output, bitmap(all), 0o644))
}

// read is the GeoJSON: the file named, or the download.
func read(args []string) ([]byte, error) {
	if len(args) > 0 {
		return os.ReadFile(args[0])
	}

	resp, err := http.Get(source)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", source, resp.Status)
	}

	return io.ReadAll(resp.Body)
}

// rings are a geometry's outlines, outer and inner alike: an even-odd fill
// treats them the same.
func rings(geo geometry) ([][]point, error) {
	switch geo.Type {
	case "Polygon":
		var poly [][]point

		return poly, json.Unmarshal(geo.Coordinates, &poly)
	case "MultiPolygon":
		var multi [][][]point
		if err := json.Unmarshal(geo.Coordinates, &multi); err != nil {
			return nil, err
		}

		var all [][]point
		for _, poly := range multi {
			all = append(all, poly...)
		}

		return all, nil
	default:
		return nil, nil
	}
}

// bitmap counts, for every cell, how many of its sample points fall inside
// a ring, by an even-odd scanline fill at each sample latitude, and writes
// the cells that are mostly land as set bits.
func bitmap(all [][]point) []byte {
	rows := north - south
	counts := make([][]int, rows)
	for i := range counts {
		counts[i] = make([]int, columns)
	}

	for row := range rows {
		for k := range samples {
			lat := float64(north-row) - (float64(k)+0.5)/samples

			var crossings []float64
			for _, ring := range all {
				for i := range ring {
					a, b := ring[i], ring[(i+1)%len(ring)]
					if (a[1] > lat) != (b[1] > lat) {
						crossings = append(crossings, a[0]+(lat-a[1])*(b[0]-a[0])/(b[1]-a[1]))
					}
				}
			}

			slices.Sort(crossings)

			for j := 0; j+1 < len(crossings); j += 2 {
				for s := range columns * samples {
					lon := -180 + (float64(s)+0.5)/samples
					if lon >= crossings[j] && lon < crossings[j+1] {
						counts[row][s/samples]++
					}
				}
			}
		}
	}

	var out []byte
	for _, line := range counts {
		var nibble, bits int
		for _, count := range line {
			nibble <<= 1
			if count*2 >= samples*samples {
				nibble |= 1
			}

			bits++
			if bits == 4 {
				out = append(out, digits[nibble])
				nibble, bits = 0, 0
			}
		}

		out = append(out, '\n')
	}

	return out
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
