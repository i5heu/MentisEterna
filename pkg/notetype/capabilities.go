package notetype

import "sort"

// HasConfig returns true if the plugin implements both ConfigValidator and ConfigSaver.
func HasConfig(p Plugin) bool {
	_, v := p.(ConfigValidator)
	_, s := p.(ConfigSaver)
	return v && s
}

// HasView returns true if the plugin implements ViewBuilder.
func HasView(p Plugin) bool {
	_, ok := p.(ViewBuilder)
	return ok
}

// HasActions returns true if the plugin implements ActionHandler.
func HasActions(p Plugin) bool {
	_, ok := p.(ActionHandler)
	return ok
}

// ListManifests returns all registered manifests sorted by SortOrder, then Label, then ID.
// The returned slice is freshly allocated.
func ListManifests() []Manifest {
	var out []Manifest
	for _, p := range Registry {
		m := p.Manifest()
		out = append(out, m)
	}
	// Sort by SortOrder, then Label, then ID.
	sort.Slice(out, func(i, j int) bool {
		if out[i].SortOrder != out[j].SortOrder {
			return out[i].SortOrder < out[j].SortOrder
		}
		if out[i].Label != out[j].Label {
			return out[i].Label < out[j].Label
		}
		return out[i].ID < out[j].ID
	})
	return out
}
