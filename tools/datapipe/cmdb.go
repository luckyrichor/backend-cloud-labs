package datapipe

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
)

// DegreeStats is the subset of datasets/stats/graph-degree.json the CMDB generator uses.
type DegreeStats struct {
	Alibaba struct {
		ServiceOut   [][2]int `json:"service_out_degree"`
		ServiceIn    [][2]int `json:"service_in_degree"`
		StorageOut   [][2]int `json:"service_storage_out_degree"`
		StorageIn    [][2]int `json:"storage_in_degree"`
		Services     int      `json:"services"`
		StorageNodes int      `json:"storage_nodes"`
	} `json:"alibaba"`
}

func LoadDegreeStats(path string) (*DegreeStats, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var d DegreeStats
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	if len(d.Alibaba.ServiceOut) == 0 || len(d.Alibaba.StorageIn) == 0 || d.Alibaba.Services == 0 {
		return nil, errors.New("degree stats missing Alibaba histograms")
	}
	return &d, nil
}

// CMDBParams: kind shares of each tenant's CIs. Service:datastore follows the Alibaba
// ratio of callers to storage callees; host and container shares are chosen, not measured.
type CMDBParams struct {
	CIs            int     `json:"cis"`
	Tenants        int     `json:"tenants"`
	HostShare      float64 `json:"host_share"`
	ServiceShare   float64 `json:"service_share"`
	DatastoreShare float64 `json:"datastore_share"`
	// containers take the remainder
}

type ciRecord struct {
	ID         int64  `json:"id"`
	Tenant     string `json:"tenant"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Attributes any    `json:"attributes"`
}

type relRecord struct {
	From   int64  `json:"from"`
	To     int64  `json:"to"`
	Kind   string `json:"kind"`
	Tenant string `json:"tenant"`
}

type hostAttr struct {
	CPU   int    `json:"cpu"`
	MemGB int    `json:"mem_gb"`
	Zone  string `json:"zone"`
}
type containerAttr struct {
	CPULimit float64 `json:"cpu_limit"`
}
type serviceAttr struct {
	Tier string `json:"tier"`
}
type datastoreAttr struct {
	Engine string `json:"engine"`
}

type CMDBStats struct {
	CIsPerKind        map[string]int64 `json:"cis_per_kind"`
	RelationsPerKind  map[string]int64 `json:"relations_per_kind"`
	RelationsPerCI    float64          `json:"relations_per_ci"`
	DependsOnOut      Summary          `json:"depends_on_out"`
	DependsOnIn       Summary          `json:"depends_on_in"`
	UsesOut           Summary          `json:"uses_out"`
	SourceDependsMean float64          `json:"source_service_out_mean"`
	SourceUsesMean    float64          `json:"source_storage_out_mean"`
	CappedDraws       int64            `json:"capped_degree_draws"`
	CappedEdgesLost   int64            `json:"capped_edges_lost"`
}

// weighted samples distinct indices with probability proportional to integer weights.
type weighted struct {
	cum   []int64
	total int64
	pos   int // indices with positive weight
}

func newWeighted(w []int) weighted {
	s := weighted{cum: make([]int64, len(w))}
	for i, v := range w {
		s.total += int64(v)
		s.cum[i] = s.total
		if v > 0 {
			s.pos++
		}
	}
	return s
}

func (s weighted) draw(r *rand.Rand) int {
	x := r.Int64N(s.total)
	return sort.Search(len(s.cum), func(i int) bool { return s.cum[i] > x })
}

// WriteCMDB writes <prefix>-cis.jsonl and <prefix>-relations.jsonl. Tenants are equal-sized;
// relations never cross tenants. Every container is an instance_of one service and runs_on
// one host; each datastore runs_on one host. Service depends_on and uses degrees are drawn
// from the Alibaba out-degree histograms, targets chosen proportionally to an in-degree
// weight drawn from the matching in-degree histogram (a Chung–Lu style construction).
// A draw larger than the available targets is capped and counted, never silently dropped.
func WriteCMDB(d *DegreeStats, dir, prefix string, seed uint64, p CMDBParams) ([]Artifact, CMDBStats, error) {
	if p.CIs%p.Tenants != 0 {
		return nil, CMDBStats{}, fmt.Errorf("cis %d not divisible by tenants %d", p.CIs, p.Tenants)
	}
	r := stream(seed, fmt.Sprintf("cmdb/%d", p.CIs))
	ci, err := createHashed(filepath.Join(dir, prefix+"-cis.jsonl"))
	if err != nil {
		return nil, CMDBStats{}, err
	}
	rel, err := createHashed(filepath.Join(dir, prefix+"-relations.jsonl"))
	if err != nil {
		ci.Abort()
		return nil, CMDBStats{}, err
	}
	fail := func(e error) ([]Artifact, CMDBStats, error) { ci.Abort(); rel.Abort(); return nil, CMDBStats{}, e }
	outH, inH := NewHistogram(d.Alibaba.ServiceOut), NewHistogram(d.Alibaba.ServiceIn)
	useH, usedH := NewHistogram(d.Alibaba.StorageOut), NewHistogram(d.Alibaba.StorageIn)
	st := CMDBStats{CIsPerKind: map[string]int64{}, RelationsPerKind: map[string]int64{}, SourceDependsMean: outH.Mean(), SourceUsesMean: useH.Mean()}
	ciEnc, relEnc := json.NewEncoder(ci), json.NewEncoder(rel)
	var nextID int64 = 1
	var nRel int64
	var depOut, depIn, useOut []int
	per := p.CIs / p.Tenants
	for t := 1; t <= p.Tenants; t++ {
		tenant := fmt.Sprintf("t%02d", t)
		nh := max(1, int(float64(per)*p.HostShare))
		ns := max(2, int(float64(per)*p.ServiceShare))
		nd := max(1, int(float64(per)*p.DatastoreShare))
		nc := per - nh - ns - nd
		if nc < ns {
			return fail(fmt.Errorf("tenant size %d leaves %d containers for %d services", per, nc, ns))
		}
		emit := func(kind string, n int, attr func(i int) any) []int64 {
			ids := make([]int64, n)
			for i := range n {
				ids[i] = nextID
				if err == nil {
					err = ciEnc.Encode(ciRecord{nextID, tenant, kind, fmt.Sprintf("%s-%s-%06d", kind, tenant, i), attr(i)})
				}
				nextID++
			}
			st.CIsPerKind[kind] += int64(n)
			return ids
		}
		zones := []string{"z1", "z2", "z3"}
		hosts := emit("host", nh, func(int) any {
			cpu := []int{8, 16, 32, 64}[r.IntN(4)]
			return hostAttr{cpu, cpu * 4, zones[r.IntN(3)]}
		})
		services := emit("service", ns, func(int) any { return serviceAttr{[]string{"web", "app", "core"}[r.IntN(3)]} })
		stores := emit("datastore", nd, func(int) any { return datastoreAttr{[]string{"mysql", "cache"}[r.IntN(2)]} })
		containers := emit("container", nc, func(int) any { return containerAttr{[]float64{0.5, 1, 2, 4}[r.IntN(4)]} })
		if err != nil {
			return fail(err)
		}
		edge := func(from, to int64, kind string) {
			if err == nil {
				err = relEnc.Encode(relRecord{from, to, kind, tenant})
			}
			st.RelationsPerKind[kind]++
			nRel++
		}
		for i, c := range containers {
			svc := i
			if i >= ns {
				svc = r.IntN(ns)
			}
			edge(c, services[svc], "instance_of")
			edge(c, hosts[r.IntN(nh)], "runs_on")
		}
		for _, s := range stores {
			edge(s, hosts[r.IntN(nh)], "runs_on")
		}
		// depends_on among services, then uses from services to datastores.
		inW := make([]int, ns)
		for i := range inW {
			inW[i] = inH.Sample(r)
		}
		svcW := newWeighted(inW)
		in := make([]int, ns)
		storeW := make([]int, nd)
		for i := range storeW {
			storeW[i] = usedH.Sample(r)
		}
		stW := newWeighted(storeW)
		for i, s := range services {
			// Exclude self from the available targets when it carries weight.
			avail := svcW.pos
			if inW[i] > 0 {
				avail--
			}
			k := outH.Sample(r)
			if k > avail {
				st.CappedDraws++
				st.CappedEdgesLost += int64(k - avail)
				k = avail
			}
			picked := map[int]bool{}
			for len(picked) < k {
				j := svcW.draw(r)
				if j == i || picked[j] {
					continue
				}
				picked[j] = true
				in[j]++
			}
			for _, j := range sortedInts(picked) {
				edge(s, services[j], "depends_on")
			}
			depOut = append(depOut, k)

			u := useH.Sample(r)
			if u > stW.pos {
				st.CappedDraws++
				st.CappedEdgesLost += int64(u - stW.pos)
				u = stW.pos
			}
			picked = map[int]bool{}
			for len(picked) < u {
				picked[stW.draw(r)] = true
			}
			for _, j := range sortedInts(picked) {
				edge(s, stores[j], "uses")
			}
			useOut = append(useOut, u)
		}
		depIn = append(depIn, in...)
		if err != nil {
			return fail(err)
		}
	}
	ciArt, err := ci.Finish(nextID - 1)
	if err != nil {
		rel.Abort()
		return nil, st, err
	}
	relArt, err := rel.Finish(nRel)
	if err != nil {
		return nil, st, err
	}
	st.RelationsPerCI = float64(nRel) / float64(p.CIs)
	st.DependsOnOut, st.DependsOnIn, st.UsesOut = Summarize(depOut), Summarize(depIn), Summarize(useOut)
	return []Artifact{ciArt, relArt}, st, nil
}

func sortedInts(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
