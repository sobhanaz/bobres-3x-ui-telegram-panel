package server

import (
	"context"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
)

// Help content (proto/core/v1/help.proto): what the bot's FAQ and Tutorials
// screens read. The customer is named so that a banned one is refused as
// everywhere else, and so that their language is the default.

func helpEntryToProto(v *domain.HelpView) *corev1.HelpEntry {
	out := &corev1.HelpEntry{Id: v.ID, Kind: v.Kind, Title: v.Title, Body: v.Body, Language: v.Language}
	for _, b := range v.Buttons {
		out.Buttons = append(out.Buttons, &corev1.HelpButton{Label: b.Label, Url: b.URL})
	}
	return out
}

// ListHelpEntries lists the enabled entries of a kind in the customer's
// language (the other where a translation is missing), in the owner's order.
func (s *Server) ListHelpEntries(ctx context.Context, req *corev1.ListHelpEntriesRequest) (*corev1.ListHelpEntriesResponse, error) {
	views, err := s.dom.HelpFor(ctx, req.GetUserId(), req.GetKind(), req.GetLanguage())
	if err != nil {
		return nil, fail(err)
	}
	out := &corev1.ListHelpEntriesResponse{Entries: make([]*corev1.HelpEntry, 0, len(views))}
	for i := range views {
		out.Entries = append(out.Entries, helpEntryToProto(&views[i]))
	}
	return out, nil
}

// GetHelpEntry reads one enabled entry as the customer does (NOT_FOUND when it
// is gone or switched off).
func (s *Server) GetHelpEntry(ctx context.Context, req *corev1.GetHelpEntryRequest) (*corev1.HelpEntry, error) {
	v, err := s.dom.HelpEntryFor(ctx, req.GetUserId(), req.GetId(), req.GetLanguage())
	if err != nil {
		return nil, fail(err)
	}
	return helpEntryToProto(v), nil
}
