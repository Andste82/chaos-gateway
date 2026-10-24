package apply_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// withHostnameRule gives the configuration a drop rule for a hostname (plan §2.4, M20).
func withHostnameRule(host string) func(*model.Configuration, *compiler.Input) {
	return func(cfg *model.Configuration, _ *compiler.Input) {
		id := uuid.MustParse("a1000000-0000-4000-8000-000000000001")
		global := model.ScopeGlobal(true)
		h := host
		rules := map[string]model.AccessRule{id.String(): {
			Action: model.AccessActionDrop, Source: model.Scope{Global: &global},
			Destination: &model.Destination{Hostname: &h},
		}}
		cfg.AccessRules = &rules
		cfg.AccessRuleOrder = &[]uuid.UUID{id}
	}
}

func dnsElements(set string, elems ...executor.DNSElement) *executor.NftDNSElements {
	return &executor.NftDNSElements{Updates: []executor.DNSSetUpdate{{Set: set, Elements: elems}}}
}

// Plan §2.6, §3.2 (S11): the elements the DNS proxy reported are in the kernel's DNS-derived set, and no apply,
// full or with a changed rule list around it, flushes the set, refills it or fails verify because of it.
func TestDNSDerivedElementsSurviveEveryApplyAndVerifyDoesNotCompareThem(t *testing.T) {
	e := newEnv(t)
	tg := e.compile(withHostnameRule("broker.example.com"))
	if len(tg.DNSSets) != 1 {
		t.Fatalf("%+v", tg.DNSSets)
	}
	set := tg.DNSSets[0]
	e.apply(tg)
	if _, err := e.ex.Do(context.Background(), dnsElements(set.Name,
		executor.DNSElement{Client: "10.10.0.42", Addr: "203.0.113.7", TimeoutSeconds: 600},
		executor.DNSElement{Client: "10.10.0.43", Addr: "203.0.113.8", TimeoutSeconds: 600})); err != nil {
		t.Fatal(err)
	}
	if got := e.k.SetElements(set.Name); len(got) != 2 {
		t.Fatalf("elements %v", got)
	}
	// the same list, then more rules around it: the elements stay, and verify passes every time
	for i := 0; i < 3; i++ {
		e.apply(e.compile(withHostnameRule("broker.example.com")))
	}
	e.apply(e.compile(withHostnameRule("broker.example.com"), func(cfg *model.Configuration, _ *compiler.Input) {
		id := uuid.MustParse("a1000000-0000-4000-8000-000000000002")
		p := model.Protocol("tcp")
		(*cfg.AccessRules)[id.String()] = model.AccessRule{Action: model.AccessActionDrop, Protocol: &p, Ports: &[]int{9999},
			Source: model.Scope{Global: ptr(model.ScopeGlobal(true))}}
		*cfg.AccessRuleOrder = append(*cfg.AccessRuleOrder, id)
	}))
	s, err := apply.ReadState(context.Background(), e.exec(), "", apply.Want{})
	if err != nil {
		t.Fatal(err)
	}
	got := s.Nft.Set(set.Name)
	if got == nil || strings.Join(got.Elements(), ",") != "10.10.0.42 . 203.0.113.7,10.10.0.43 . 203.0.113.8" {
		t.Fatalf("the elements after the applies: %+v", got)
	}
	if typ := string(got.Type); typ != `["ipv4_addr","ipv4_addr"]` || strings.Join(got.Flags, ",") != "timeout" {
		t.Errorf("the set is %s %v", typ, got.Flags)
	}
	// writing an element again does not duplicate it (the kernel keeps what is there)
	if _, err := e.ex.Do(context.Background(), dnsElements(set.Name, executor.DNSElement{Client: "10.10.0.42", Addr: "203.0.113.7", TimeoutSeconds: 900})); err != nil {
		t.Fatal(err)
	}
	if n := len(e.k.SetElements(set.Name)); n != 2 {
		t.Errorf("%d elements", n)
	}
	// a refresh deletes the element and adds it again, and one for an element that is not there is an add
	if _, err := e.ex.Do(context.Background(), dnsElements(set.Name,
		executor.DNSElement{Client: "10.10.0.42", Addr: "203.0.113.7", TimeoutSeconds: 900, Replace: true},
		executor.DNSElement{Client: "10.10.0.50", Addr: "203.0.113.9", TimeoutSeconds: 900, Replace: true})); err != nil {
		t.Fatal(err)
	}
	if n := len(e.k.SetElements(set.Name)); n != 3 {
		t.Errorf("%d elements after a refresh", n)
	}
}

// A selector that no rule uses any more takes its set, with the elements, out of the kernel; a new one starts empty.
func TestTheSetOfASelectorThatIsNotUsedAnyMoreGoesAndANewOneStartsEmpty(t *testing.T) {
	e := newEnv(t)
	tg := e.compile(withHostnameRule("broker.example.com"))
	e.apply(tg)
	old := tg.DNSSets[0].Name
	if _, err := e.ex.Do(context.Background(), dnsElements(old, executor.DNSElement{Client: "10.10.0.42", Addr: "203.0.113.7", TimeoutSeconds: 600})); err != nil {
		t.Fatal(err)
	}
	tg2 := e.compile(withHostnameRule("other.example.com"))
	e.apply(tg2)
	s, err := apply.ReadState(context.Background(), e.exec(), "", apply.Want{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Nft.Set(old) != nil || s.Nft.Set(tg2.DNSSets[0].Name) == nil || len(s.Nft.Set(tg2.DNSSets[0].Name).Elem) != 0 {
		t.Errorf("sets %+v", s.Nft.Objects)
	}
	// an update for the set that went fails: there is no such set (the engine never sends one)
	if _, err := e.ex.Do(context.Background(), dnsElements(old, executor.DNSElement{Client: "10.10.0.42", Addr: "203.0.113.7", TimeoutSeconds: 600})); err == nil {
		t.Error("an element was written into a set that is not there")
	}
}
