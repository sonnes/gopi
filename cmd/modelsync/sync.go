package main

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/sonnes/gopi/internal/modelsfile"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/catalog"
)

// source is where the generator gets the models of one provider or
// driver ID.
type source struct {
	// ID is the provider or driver ID of models.json.
	ID string
	// Kind is the kind of a new entry.
	Kind string
	// List lists the models, or is nil if no list is available. Skip
	// says why.
	List func(context.Context) ([]ai.Model, error)
	Skip string
	// Dev is the provider ID of models.dev, or empty for none.
	Dev string
	// DevAdds makes models.dev add each of its models with tools that
	// the entry does not have. It is for a provider whose own list has
	// no prices, such as Fireworks.
	DevAdds bool
	// DevAdmits adds a listed model only if models.dev has it with
	// tools. It is for a list that holds models that are not for chat,
	// such as the list of OpenAI.
	DevAdmits bool
}

// syncFile returns f with the models of each source merged in, and a
// report for a person to read. A price in f always stays. The report
// ends with each price that differs from models.dev.
func syncFile(
	ctx context.Context,
	f modelsfile.File,
	sources []source,
	dev devCatalog,
	prune bool,
) (modelsfile.File, []string) {
	out := maps.Clone(f)
	if out == nil {
		out = make(modelsfile.File)
	}
	var report, diffs []string
	for _, s := range sources {
		e, lines, d := syncEntry(ctx, f[s.ID], s, dev[s.Dev], prune)
		report = append(report, lines...)
		diffs = append(diffs, d...)
		if len(e.Models) > 0 {
			out[s.ID] = e
		}
	}
	if len(diffs) > 0 {
		report = append(report, "prices that differ from models.dev:")
		report = append(report, diffs...)
	}
	return out, report
}

// syncEntry returns old with the models of s and of models.dev, the
// report lines, and the lines of the prices that differ from
// models.dev.
func syncEntry(
	ctx context.Context,
	old modelsfile.Entry,
	s source,
	dp devProvider,
	prune bool,
) (modelsfile.Entry, []string, []string) {
	byID := make(map[string]modelsfile.Model, len(old.Models))
	for _, m := range old.Models {
		byID[m.ID] = m
	}

	var lines []string
	var listed []ai.Model
	if s.List == nil {
		lines = append(lines, fmt.Sprintf("%s: no list: %s", s.ID, s.Skip))
	} else if l, err := s.List(ctx); err != nil {
		lines = append(lines, fmt.Sprintf("%s: the list failed: %v", s.ID, err))
	} else {
		listed = l
		lines = append(lines, mergeList(byID, listed, s, dp, prune)...)
	}

	if s.DevAdds {
		for _, id := range slices.Sorted(maps.Keys(dp.Models)) {
			m := dp.Models[id]
			if _, ok := byID[id]; ok || !m.ToolCall {
				continue
			}
			byID[id] = modelsfile.Model{Model: m.model()}
			lines = append(lines, fmt.Sprintf("  + %s (models.dev)", id))
		}
	}

	var filled, diffs []string
	for _, id := range slices.Sorted(maps.Keys(byID)) {
		d, ok := dp.Models[id]
		if !ok {
			continue
		}
		m := byID[id]
		next := m
		next.Model = fill(m.Model, d.model())
		if !reflect.DeepEqual(next, m) {
			filled = append(filled, id)
			byID[id] = next
		}
		diffs = append(diffs, priceDiffs(s.ID+"/"+id, next.Pricing, d.model().Pricing)...)
	}
	if len(filled) > 0 {
		lines = append(lines, "  filled from models.dev: "+strings.Join(filled, ", "))
	}

	e := modelsfile.Entry{
		Kind:    cmp.Or(old.Kind, s.Kind),
		Default: old.Default,
	}
	for _, id := range slices.Sorted(maps.Keys(byID)) {
		e.Models = append(e.Models, byID[id])
	}
	// The list of a CLI puts its default first. A provider list has no
	// default, so a person sets the default of a provider.
	isDriver := s.Kind == modelsfile.KindDriver
	if isDriver && len(listed) > 0 && listed[0].ID != e.Default {
		e.Default = listed[0].ID
		lines = append(lines, fmt.Sprintf("%s: the default is now %s, the default of its list", s.ID, e.Default))
	}
	if _, ok := byID[e.Default]; !ok && len(e.Models) > 0 {
		e.Default = e.Models[0].ID
		lines = append(lines, fmt.Sprintf("%s: the default is now %s. Check it.", s.ID, e.Default))
	}
	return e, lines, diffs
}

// mergeList merges listed into byID with [catalog.MergeModel], or with
// [catalog.MergeDriverModel] for a driver, and returns the report
// lines. A note stays. With prune, a model that the list does not have
// leaves byID. An empty list prunes nothing, because a provider that
// serves models does not list none of them.
func mergeList(
	byID map[string]modelsfile.Model,
	listed []ai.Model,
	s source,
	dp devProvider,
	prune bool,
) []string {
	var added, changed, removed []string
	ignored := 0
	inList := make(map[string]bool, len(listed))
	for _, l := range listed {
		inList[l.ID] = true
		old, ok := byID[l.ID]
		if !ok {
			if s.DevAdmits && !dp.Models[l.ID].ToolCall {
				ignored++
				continue
			}
			byID[l.ID] = modelsfile.Model{Model: l}
			added = append(added, l.ID)
			continue
		}
		merge := catalog.MergeModel
		if s.Kind == modelsfile.KindDriver {
			merge = catalog.MergeDriverModel
		}
		next := modelsfile.Model{
			Model: merge(old.Model, l),
			Note:  old.Note,
		}
		if !reflect.DeepEqual(next, old) {
			byID[l.ID] = next
			changed = append(changed, l.ID)
		}
	}
	emptyPrune := prune && len(listed) == 0
	if prune && !emptyPrune {
		for id := range byID {
			if !inList[id] {
				delete(byID, id)
				removed = append(removed, id)
			}
		}
	}

	lines := []string{fmt.Sprintf(
		"%s: listed %d: %d added, %d changed, %d removed",
		s.ID,
		len(listed),
		len(added),
		len(changed),
		len(removed),
	)}
	for _, group := range []struct {
		mark string
		ids  []string
	}{{"+", added}, {"~", changed}, {"-", removed}} {
		slices.Sort(group.ids)
		for _, id := range group.ids {
			lines = append(lines, "  "+group.mark+" "+id)
		}
	}
	if emptyPrune {
		lines = append(lines, fmt.Sprintf("%s: the list is empty, so the prune removes nothing", s.ID))
	}
	if ignored > 0 {
		lines = append(lines, fmt.Sprintf("%s: ignored %d listed models that models.dev does not have with tools", s.ID, ignored))
	}
	return lines
}

// fill returns m with each empty value taken from d. A model with a
// price keeps all of its prices. A model with thinking levels keeps its
// thinking: a false Reasoning is a choice, and JSON cannot tell it from
// a missing value.
func fill(m, d ai.Model) ai.Model {
	m.Name = cmp.Or(m.Name, d.Name)
	m.MaxTokens = cmp.Or(m.MaxTokens, d.MaxTokens)
	m.ContextWindow = cmp.Or(m.ContextWindow, d.ContextWindow)
	if len(m.Input) == 0 {
		m.Input = d.Input
	}
	if len(m.ThinkingLevels) == 0 {
		m.Reasoning = m.Reasoning || d.Reasoning
		m.ThinkingLevels = d.ThinkingLevels
	}
	if !hasPrices(m.Pricing) {
		m.Pricing = d.Pricing
	}
	return m
}

// hasPrices reports whether p has any price.
func hasPrices(p ai.Pricing) bool {
	base := p.Input + p.Output + p.CacheRead + p.CacheWrite + p.CacheWriteLong
	return base != 0 || len(p.Tiers) > 0
}

// priceDiffs returns a line for each base price of file that differs
// from dev. A price that models.dev does not have is not compared.
func priceDiffs(spec string, file, dev ai.Pricing) []string {
	if !hasPrices(file) || !hasPrices(dev) {
		return nil
	}
	var lines []string
	for _, p := range []struct {
		name      string
		file, dev float64
	}{
		{"input", file.Input, dev.Input},
		{"output", file.Output, dev.Output},
		{"cache_read", file.CacheRead, dev.CacheRead},
		{"cache_write", file.CacheWrite, dev.CacheWrite},
	} {
		if p.dev != 0 && p.file != p.dev {
			lines = append(lines, fmt.Sprintf("  %s: %s %g in the file, %g in models.dev", spec, p.name, p.file, p.dev))
		}
	}
	return lines
}
