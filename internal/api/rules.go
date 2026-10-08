package api

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// ruleCounter is the counter of a compiled access rule: the packets and bytes of the new connections the
// rule decided. Nil when the counters could not be read. The epoch changes when the counter starts over
// (the rule is new, or the table was made anew).
func (oc overlayContext) ruleCounter(r compiler.AccessRule) *model.Counter {
	if oc.counters == nil {
		return nil
	}
	v, ok := oc.counters[r.Counter]
	if !ok {
		return nil
	}
	return &model.Counter{Packets: v.Packets, Bytes: v.Bytes, Epoch: oc.snap.RuleEpochs[r.Key]}
}

// systemRules are the rules the gateway adds that no rule or overlay can override, with their counters.
func (oc overlayContext) systemRules() []model.SystemAccessRule {
	r := compiler.AntiLockoutRule
	sr := model.SystemAccessRule{Key: r.Key, Name: r.Name, Description: r.Description}
	if v, ok := oc.counters[r.Counter]; ok && oc.counters != nil {
		sr.Counters = &model.Counter{Packets: v.Packets, Bytes: v.Bytes, Epoch: oc.snap.CounterEpoch}
	}
	return []model.SystemAccessRule{sr}
}

// ruleViews builds the views of the configured access rules of a revision in evaluation order (the
// order of `access_rule_order`). Only the revision the kernel runs has counters; the state of a rule
// there says whether it is in the packet path.
func (s *Server) ruleViews(ctx context.Context, v view) (views []model.AccessRuleView, oc overlayContext) {
	if v.active {
		oc = s.readOverlayContext(ctx, true)
	}
	rules := map[string]model.AccessRule{}
	if v.cfg.AccessRules != nil {
		rules = *v.cfg.AccessRules
	}
	var order []uuid.UUID
	if v.cfg.AccessRuleOrder != nil {
		order = *v.cfg.AccessRuleOrder
	}
	seen := map[string]bool{}
	add := func(pos int, id uuid.UUID, r model.AccessRule) {
		rv := model.AccessRuleView{Id: id, Position: pos, Config: r}
		st := model.EffectStateEffective
		switch {
		case r.Enabled != nil && !*r.Enabled:
			st = model.EffectStateDisabled
		case v.active && oc.snap != nil:
			if cr, ok := oc.snap.Access.Rule("config:" + id.String()); ok {
				rv.Counters = oc.ruleCounter(*cr)
			} else {
				// enabled, but not in the packet path: a rule that names a hostname before M20, or a
				// revision the kernel does not run yet
				st = model.EffectStateDisabled
			}
		}
		rv.State = &st
		views = append(views, rv)
	}
	for pos, id := range order {
		r, ok := rules[id.String()]
		if !ok {
			continue
		}
		seen[id.String()] = true
		add(pos, id, r)
	}
	// a rule the order does not name (a hand-edited configuration the validation would refuse) is still listed
	for key, r := range rules {
		if id, err := uuid.Parse(key); err == nil && !seen[key] {
			add(len(order), id, r)
		}
	}
	return views, oc
}

func ruleSortKey(rv model.AccessRuleView) string {
	return fmt.Sprintf("%08d/%s", rv.Position, rv.Id)
}

// ListAccessRules implements GET /rules: the configured rules in the order they are evaluated, with their
// state and counters, behind the system rules that cannot be overridden. Overlay rules are listed under
// /overlays?kind=rule.
func (s *Server) ListAccessRules(c *gin.Context, params model.ListAccessRulesParams) {
	v, ok := s.viewOf(c, params.Revision)
	if !ok {
		return
	}
	all, oc := s.ruleViews(contextOf(c), v)
	items, next, err := page(all, ruleSortKey, params.Cursor, params.Limit)
	if err != nil {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "%v", err))
		return
	}
	if items == nil {
		items = []model.AccessRuleView{}
	}
	body := model.AccessRulePage{Items: items, NextCursor: next}
	sys := oc.systemRules()
	if !v.active {
		sys = []model.SystemAccessRule{{Key: compiler.AntiLockoutRule.Key, Name: compiler.AntiLockoutRule.Name, Description: compiler.AntiLockoutRule.Description}}
	}
	body.SystemRules = &sys
	c.JSON(200, body)
}

// GetAccessRule implements GET /rules/{ruleId}: by id or by name.
func (s *Server) GetAccessRule(c *gin.Context, ruleId model.Ref, params model.GetAccessRuleParams) {
	v, ok := s.viewOf(c, params.Revision)
	if !ok {
		return
	}
	all, _ := s.ruleViews(contextOf(c), v)
	for _, rv := range all {
		if strings.EqualFold(rv.Id.String(), ruleId) || (rv.Config.Name != nil && strings.EqualFold(*rv.Config.Name, ruleId)) {
			c.JSON(200, rv)
			return
		}
	}
	s.write(c, notFound("access rule", ruleId))
}

// previewRules are the effective rules of a preview as the spec's PreviewRule.
func previewRules(rules []engine.PreviewRule) []model.PreviewRule {
	out := make([]model.PreviewRule, 0, len(rules))
	for _, r := range rules {
		pr := model.PreviewRule{Key: r.Key, Layer: model.PreviewRuleLayer(r.Layer), Position: r.Position,
			Action: model.AccessAction(r.Action), Protocol: model.Protocol(r.Protocol)}
		if id, err := uuid.Parse(r.ID); err == nil {
			pr.Id = &id
		}
		if r.Name != "" {
			pr.Name = ptr(r.Name)
		}
		if r.CutExisting {
			pr.CutExisting = ptr(true)
		}
		if r.Scope != "" {
			pr.Scope = ptr(r.Scope)
		}
		if r.Destination != "" {
			pr.Destination = ptr(r.Destination)
		}
		if len(r.Ports) > 0 {
			ports := make([]string, 0, len(r.Ports))
			for _, p := range r.Ports {
				if p.From == p.To {
					ports = append(ports, strconv.Itoa(p.From))
				} else {
					ports = append(ports, fmt.Sprintf("%d-%d", p.From, p.To))
				}
			}
			pr.Ports = &ports
		}
		if r.New {
			pr.New = ptr(true)
		}
		out = append(out, pr)
	}
	return out
}
