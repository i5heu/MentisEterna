package notetype

import "sort"

// HasManifest returns the Manifest if the plugin implements ManifestProvider, or nil.
func HasManifest(nt NoteType) *Manifest {
	if mp, ok := nt.(ManifestProvider); ok {
		m := mp.Manifest()
		return &m
	}
	return nil
}

// HasConfig returns true if the plugin implements both ConfigValidator and ConfigSaver.
func HasConfig(nt NoteType) bool {
	_, v := nt.(ConfigValidator)
	_, s := nt.(ConfigSaver)
	return v && s
}

// HasView returns true if the plugin implements ViewBuilder.
func HasView(nt NoteType) bool {
	_, ok := nt.(ViewBuilder)
	return ok
}

// HasActions returns true if the plugin implements ActionHandler.
func HasActions(nt NoteType) bool {
	_, ok := nt.(ActionHandler)
	return ok
}

// ListManifests returns all registered manifests sorted by SortOrder, then Label, then ID.
// The returned slice is freshly allocated.
func ListManifests() []Manifest {
	var out []Manifest
	for _, nt := range Registry {
		if mp, ok := nt.(ManifestProvider); ok {
			m := mp.Manifest()
			out = append(out, m)
		}
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
