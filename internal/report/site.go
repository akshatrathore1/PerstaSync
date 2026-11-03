package report

import (
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"perstasync/internal/protocol"
)

func WriteSites(writer io.Writer, sites []protocol.Site) error {
	ordered := append(make([]protocol.Site, 0, len(sites)), sites...)
	for _, site := range ordered {
		if site.File == "" || site.File == "." || site.File == ".." || path.IsAbs(site.File) || path.Clean(site.File) != site.File || strings.HasPrefix(site.File, "../") || strings.ContainsAny(site.File, "\\:") || site.Offset < 0 {
			return fmt.Errorf("site requires a normalized relative source path and a nonnegative byte offset")
		}
		if !site.Level.Valid() {
			return fmt.Errorf("site has an invalid conformance level")
		}
		if (site.Level == protocol.L1Unknown || site.Level == protocol.Undetermined) && strings.TrimSpace(site.Reason) == "" {
			return fmt.Errorf("unresolved site requires a reason")
		}
	}
	sort.Slice(ordered, func(first, second int) bool {
		if ordered[first].File != ordered[second].File {
			return ordered[first].File < ordered[second].File
		}
		return ordered[first].Offset < ordered[second].Offset
	})
	for index := 1; index < len(ordered); index++ {
		if ordered[index].File == ordered[index-1].File && ordered[index].Offset == ordered[index-1].Offset {
			return fmt.Errorf("duplicate publication coordinate")
		}
	}
	return json.NewEncoder(writer).Encode(ordered)
}
