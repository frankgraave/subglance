package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/frankgraave/subglance/internal/store"
)

// Routing rules send the alerts of every monitor carrying one tag to a set of
// channels. They add up: an alert goes to the monitor's own channels plus
// every matching rule's, and the default only when that is nobody. See
// store.AlertChannels.

type routingRuleRequest struct {
	TagKey     string  `json:"tag_key"`
	TagValue   string  `json:"tag_value"`
	ChannelIDs []int64 `json:"channel_ids"`
}

type routingRuleResponse struct {
	ID                 int64   `json:"id"`
	TagKey             string  `json:"tag_key"`
	TagValue           string  `json:"tag_value"`
	ChannelIDs         []int64 `json:"channel_ids"`
	ExcludedMonitorIDs []int64 `json:"excluded_monitor_ids"`
	CreatedAt          string  `json:"created_at"`
}

func toRoutingRuleResponse(r store.RoutingRule) routingRuleResponse {
	out := routingRuleResponse{
		ID: r.ID, TagKey: r.TagKey, TagValue: r.TagValue,
		ChannelIDs: r.ChannelIDs, ExcludedMonitorIDs: r.ExcludedMonitorIDs,
		CreatedAt: r.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	// Empty lists go out as [], never null: a client should not need to
	// know that "no channels" and "field missing" are the same thing here.
	if out.ChannelIDs == nil {
		out.ChannelIDs = []int64{}
	}
	if out.ExcludedMonitorIDs == nil {
		out.ExcludedMonitorIDs = []int64{}
	}
	return out
}

func (s *Server) handleListRoutingRules(w http.ResponseWriter, r *http.Request) {
	rules, err := s.db.ListRoutingRules(r.Context())
	if err != nil {
		s.log.Error("list routing rules", "error", err)
		writeError(w, http.StatusInternalServerError, "could not list routing rules")
		return
	}
	out := make([]routingRuleResponse, 0, len(rules))
	for _, rule := range rules {
		out = append(out, toRoutingRuleResponse(rule))
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": out})
}

// decodeRoutingRule reads and normalises a rule body. It writes the 400
// itself and reports false when the body is unusable.
func decodeRoutingRule(w http.ResponseWriter, r *http.Request) (store.RoutingRule, bool) {
	var req routingRuleRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return store.RoutingRule{}, false
	}
	key, value, err := store.NormaliseRoutingTag(req.TagKey, req.TagValue)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return store.RoutingRule{}, false
	}
	return store.RoutingRule{TagKey: key, TagValue: value, ChannelIDs: req.ChannelIDs}, true
}

// writeRoutingRuleError maps the store's refusals to statuses. It reports
// false when err was nil and nothing was written.
func (s *Server) writeRoutingRuleError(w http.ResponseWriter, err error, action string, id int64) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrRoutingRuleExists):
		writeError(w, http.StatusConflict, "a routing rule for this tag already exists; add the channels to that rule")
	case errors.Is(err, store.ErrUnknownChannel):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "routing rule not found")
	default:
		s.log.Error(action+" routing rule", "rule_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not "+action+" the routing rule")
	}
	return true
}

func (s *Server) handleCreateRoutingRule(w http.ResponseWriter, r *http.Request) {
	rule, ok := decodeRoutingRule(w, r)
	if !ok {
		return
	}
	created, err := s.db.CreateRoutingRule(r.Context(), rule)
	if s.writeRoutingRuleError(w, err, "create", 0) {
		return
	}
	s.log.Info("routing rule created", "rule_id", created.ID, "tag_key", created.TagKey,
		"tag_value", created.TagValue, "channels", len(created.ChannelIDs))
	writeJSON(w, http.StatusCreated, toRoutingRuleResponse(created))
}

func (s *Server) handleUpdateRoutingRule(w http.ResponseWriter, r *http.Request) {
	id, ok := ruleID(w, r)
	if !ok {
		return
	}
	rule, ok := decodeRoutingRule(w, r)
	if !ok {
		return
	}
	rule.ID = id
	updated, err := s.db.UpdateRoutingRule(r.Context(), rule)
	if s.writeRoutingRuleError(w, err, "update", id) {
		return
	}
	s.log.Info("routing rule updated", "rule_id", id, "tag_key", updated.TagKey,
		"tag_value", updated.TagValue, "channels", len(updated.ChannelIDs))
	writeJSON(w, http.StatusOK, toRoutingRuleResponse(updated))
}

func (s *Server) handleDeleteRoutingRule(w http.ResponseWriter, r *http.Request) {
	id, ok := ruleID(w, r)
	if !ok {
		return
	}
	if s.writeRoutingRuleError(w, s.db.DeleteRoutingRule(r.Context(), id), "delete", id) {
		return
	}
	s.log.Info("routing rule deleted", "rule_id", id)
	w.WriteHeader(http.StatusNoContent)
}

// handleExcludeMonitorFromRule mutes one rule for one monitor. Idempotent,
// because the state asked for is the same however often it is asked.
func (s *Server) handleExcludeMonitorFromRule(w http.ResponseWriter, r *http.Request) {
	id, monitorID, ok := ruleAndMonitorID(w, r)
	if !ok {
		return
	}
	err := s.db.ExcludeMonitorFromRule(r.Context(), id, monitorID)
	if errors.Is(err, store.ErrNotFound) {
		// The store names which one is missing; a client needs to know
		// whether to reload its rules or its monitors.
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		s.log.Error("exclude monitor from routing rule", "rule_id", id, "monitor_id", monitorID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not save the exclusion")
		return
	}
	s.log.Info("monitor excluded from routing rule", "rule_id", id, "monitor_id", monitorID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleIncludeMonitorInRule(w http.ResponseWriter, r *http.Request) {
	id, monitorID, ok := ruleAndMonitorID(w, r)
	if !ok {
		return
	}
	err := s.db.IncludeMonitorInRule(r.Context(), id, monitorID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "that monitor is not excluded from that routing rule")
		return
	}
	if err != nil {
		s.log.Error("include monitor in routing rule", "rule_id", id, "monitor_id", monitorID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not remove the exclusion")
		return
	}
	s.log.Info("monitor included in routing rule again", "rule_id", id, "monitor_id", monitorID)
	w.WriteHeader(http.StatusNoContent)
}

func ruleID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid routing rule id")
		return 0, false
	}
	return id, true
}

func ruleAndMonitorID(w http.ResponseWriter, r *http.Request) (int64, int64, bool) {
	id, ok := ruleID(w, r)
	if !ok {
		return 0, 0, false
	}
	monitorID, err := strconv.ParseInt(r.PathValue("monitor_id"), 10, 64)
	if err != nil || monitorID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid monitor id")
		return 0, 0, false
	}
	return id, monitorID, true
}
