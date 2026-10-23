package datapipe

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand/v2"
	"path/filepath"
)

// Track file for E2 bots (S3). Little-endian:
//
//	magic[8] "BCLTRK1\0" | version u32 = 1 | pattern u32 | bots u32 | frames u32 | world f32 | speed f32
//	frames × bots × { dx f32 | dy f32 }   (frame-major: one frame's intents are contiguous)
//
// Bot b only draws from its own stream, so the first n bots of a 2000-bot file are exactly
// the n-bot workload; smaller tiers are prefixes, not separate files.
const (
	TrackMagic       = "BCLTRK1\x00"
	TrackWaypoint    = 1
	TrackHotspot     = 2
	trackHeaderBytes = 32
)

type TrackParams struct {
	Pattern   uint32  `json:"pattern"`
	Bots      int     `json:"bots"`
	Frames    int     `json:"frames"`
	World     float64 `json:"world"` // waypoints lie in [-World/2, World/2]^2
	Speed     float64 `json:"speed"` // server applies at most 1 unit per frame
	MaxDwell  int     `json:"max_dwell_frames"`
	Hotspots  int     `json:"hotspots"`
	HotShare  float64 `json:"hotspot_share"`
	HotSpread float64 `json:"hotspot_spread"`
}

type bot struct {
	r            *rand.Rand
	x, y, wx, wy float64
	dwell        int
}

// spread approximates a centred normal with the Irwin–Hall sum of three uniforms
// (variance 1/4 per unit, so ×2 gives σ≈1): arithmetic only, no Exp/Log.
func spread(r *rand.Rand) float64 { return (r.Float64() + r.Float64() + r.Float64() - 1.5) * 2 }

func (p TrackParams) waypoint(r *rand.Rand, hot [][2]float64) (float64, float64) {
	half := p.World / 2
	if p.Pattern == TrackHotspot && r.Float64() < p.HotShare {
		h := hot[r.IntN(len(hot))]
		s := p.HotSpread * p.World
		return math.Max(-half, math.Min(half, h[0]+spread(r)*s)), math.Max(-half, math.Min(half, h[1]+spread(r)*s))
	}
	return (r.Float64()*2 - 1) * half, (r.Float64()*2 - 1) * half
}

type TrackStats struct {
	Name       string            `json:"name"`
	Hotspots   [][2]float64      `json:"hotspots,omitempty"`
	Idle       float64           `json:"idle_intent_share"`
	Neighbours []NeighbourSample `json:"neighbours_within_radius"`
}

// NeighbourSample is the mean number of other bots within Radius at one frame, for the
// first Bots bots: the density an AOI filter would see.
type NeighbourSample struct {
	Frame  int     `json:"frame"`
	Bots   int     `json:"bots"`
	Radius float64 `json:"radius"`
	Mean   float64 `json:"mean"`
	Max    int     `json:"max"`
}

// Tiers are the bot counts S3 runs at; all are prefixes of the generated file.
var Tiers = []int{64, 128, 256, 512, 1000, 2000}

func WriteTrack(dir, name string, seed uint64, p TrackParams, radii []float64) (Artifact, TrackStats, error) {
	st := TrackStats{Name: name}
	var hot [][2]float64
	if p.Pattern == TrackHotspot {
		hr := stream(seed, "track/hotspots")
		for range p.Hotspots {
			// Keep hotspot centres away from the edge so their spread is not clipped.
			hot = append(hot, [2]float64{(hr.Float64()*2 - 1) * p.World * .35, (hr.Float64()*2 - 1) * p.World * .35})
		}
		st.Hotspots = hot
	}
	bots := make([]bot, p.Bots)
	for i := range bots {
		b := &bots[i]
		b.r = stream(seed, fmt.Sprintf("track/%d/bot/%d", p.Pattern, i))
		b.wx, b.wy = p.waypoint(b.r, hot) // all bots join at the origin, as the server spawns them
	}
	out, err := createHashed(filepath.Join(dir, name))
	if err != nil {
		return Artifact{}, st, err
	}
	head := make([]byte, trackHeaderBytes)
	copy(head, TrackMagic)
	binary.LittleEndian.PutUint32(head[8:], 1)
	binary.LittleEndian.PutUint32(head[12:], p.Pattern)
	binary.LittleEndian.PutUint32(head[16:], uint32(p.Bots))
	binary.LittleEndian.PutUint32(head[20:], uint32(p.Frames))
	binary.LittleEndian.PutUint32(head[24:], math.Float32bits(float32(p.World)))
	binary.LittleEndian.PutUint32(head[28:], math.Float32bits(float32(p.Speed)))
	out.Write(head)
	sampleAt := map[int]bool{p.Frames / 4: true, p.Frames / 2: true, 3 * p.Frames / 4: true, p.Frames - 1: true}
	frame := make([]byte, 8*p.Bots)
	idle := 0
	for f := 0; f < p.Frames; f++ {
		for i := range bots {
			b := &bots[i]
			var dx, dy float64
			if b.dwell > 0 {
				b.dwell--
				idle++
			} else {
				ex, ey := b.wx-b.x, b.wy-b.y
				d := math.Hypot(ex, ey)
				if d <= p.Speed {
					dx, dy = ex, ey
					b.dwell = b.r.IntN(p.MaxDwell + 1)
					b.wx, b.wy = p.waypoint(b.r, hot)
				} else {
					dx, dy = ex/d*p.Speed, ey/d*p.Speed
				}
			}
			// Positions advance by the f32 values actually sent, matching what the server applies.
			fx, fy := float32(dx), float32(dy)
			b.x += float64(fx)
			b.y += float64(fy)
			binary.LittleEndian.PutUint32(frame[8*i:], math.Float32bits(fx))
			binary.LittleEndian.PutUint32(frame[8*i+4:], math.Float32bits(fy))
		}
		if _, err := out.Write(frame); err != nil {
			out.Abort()
			return Artifact{}, st, err
		}
		if sampleAt[f] {
			for _, n := range Tiers {
				if n > p.Bots {
					continue
				}
				for _, radius := range radii {
					st.Neighbours = append(st.Neighbours, neighbours(bots[:n], f, radius))
				}
			}
		}
	}
	st.Idle = float64(idle) / float64(p.Frames*p.Bots)
	art, err := out.Finish(int64(p.Frames))
	return art, st, err
}

func neighbours(bots []bot, frame int, radius float64) NeighbourSample {
	total, most := 0, 0
	r2 := radius * radius
	for i := range bots {
		c := 0
		for j := range bots {
			dx, dy := bots[i].x-bots[j].x, bots[i].y-bots[j].y
			if i != j && dx*dx+dy*dy <= r2 {
				c++
			}
		}
		total += c
		most = max(most, c)
	}
	return NeighbourSample{Frame: frame, Bots: len(bots), Radius: radius, Mean: float64(total) / float64(len(bots)), Max: most}
}
