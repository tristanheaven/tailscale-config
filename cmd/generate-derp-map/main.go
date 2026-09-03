package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/tailscale/hujson"
)

const officialMapURL = "https://controlplane.tailscale.com/derpmap/default"

type officialRegion struct {
	ID   int
	Code string
}

func main() {
	allowedPath := flag.String("allowed", "derp-allowed-regions.txt",
		"file with the allowed DERP region codes, one per line")
	flag.Parse()
	policyPath := "policy.hujson"
	if flag.NArg() > 0 {
		policyPath = flag.Arg(0)
	}
	if err := run(policyPath, *allowedPath); err != nil {
		fmt.Fprintln(os.Stderr, "generate-derp-map:", err)
		os.Exit(1)
	}
}

func run(policyPath, allowedPath string) error {
	raw, err := os.ReadFile(policyPath)
	if err != nil {
		return err
	}
	root, err := hujson.Parse(raw)
	if err != nil {
		return fmt.Errorf("parsing %s: %w", policyPath, err)
	}
	dm := root.Find("/derpMap")
	if dm == nil {
		return fmt.Errorf("%s: no derpMap; add one first", policyPath)
	}

	allowed, err := readAllowedCodes(allowedPath)
	if err != nil {
		return err
	}
	official, err := fetchOfficialMap()
	if err != nil {
		return err
	}

	disabled := make([]officialRegion, 0, len(official))
	for _, r := range official {
		if !slices.Contains(allowed, r.Code) {
			disabled = append(disabled, r)
		}
	}
	for _, code := range allowed {
		if !slices.ContainsFunc(official, func(r officialRegion) bool { return r.Code == code }) {
			fmt.Fprintf(os.Stderr, "warning: allowed region code %q is not in the official map\n", code)
		}
	}

	ids := make(map[string]any, len(disabled))
	for _, r := range disabled {
		ids[strconv.Itoa(r.ID)] = nil
	}
	block, err := json.MarshalIndent(map[string]any{"Regions": ids}, "  ", "  ")
	if err != nil {
		return err
	}
	newDM, err := hujson.Parse(block)
	if err != nil {
		return fmt.Errorf("building derpMap: %w", err)
	}
	newDM.BeforeExtra = dm.BeforeExtra
	*dm = newDM

	if err := os.WriteFile(policyPath, root.Pack(), 0o644); err != nil {
		return err
	}
	fmt.Printf("derpMap: disabled %d regions; allowed: %s\n", len(disabled), strings.Join(allowed, ", "))
	return nil
}

func fetchOfficialMap() ([]officialRegion, error) {
	res, err := http.Get(officialMapURL)
	if err != nil {
		return nil, fmt.Errorf("fetching official map: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching official map: %s", res.Status)
	}
	var m struct {
		Regions map[string]struct {
			RegionID   int    `json:"RegionID"`
			RegionCode string `json:"RegionCode"`
		} `json:"Regions"`
	}
	if err := json.NewDecoder(res.Body).Decode(&m); err != nil {
		return nil, fmt.Errorf("parsing official map: %w", err)
	}
	var regions []officialRegion
	for idStr, r := range m.Regions {
		id, err := strconv.Atoi(idStr)
		if err != nil {
			return nil, fmt.Errorf("official map: invalid region ID %q", idStr)
		}
		regions = append(regions, officialRegion{id, r.RegionCode})
	}
	sort.Slice(regions, func(i, j int) bool { return regions[i].ID < regions[j].ID })
	if len(regions) == 0 {
		return nil, fmt.Errorf("official map has no regions")
	}
	return regions, nil
}

func readAllowedCodes(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var codes []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") && !slices.Contains(codes, line) {
			codes = append(codes, line)
		}
	}
	if len(codes) == 0 {
		return nil, fmt.Errorf("no allowed region codes in %s", path)
	}
	return codes, nil
}
