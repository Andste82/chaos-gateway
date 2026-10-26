package executor

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	dnsSetA = "dns_0123456789_abcdef"
	dnsSetB = "dns_a5c9312cee_65394f"
)

func dnsOp(sets ...string) string {
	var us []string
	for _, s := range sets {
		us = append(us, `{"set":"`+s+`","elements":[{"client":"10.10.0.42","addr":"203.0.113.7","timeout_seconds":600},{"client":"10.10.0.43","addr":"203.0.113.8","timeout_seconds":601,"replace":true}]}`)
	}
	return `{"type":"nft_dns_elements","updates":[` + strings.Join(us, ",") + `]}`
}

// The operation takes the sets of DNS-derived selectors only, and bounded, valid addresses and lifetimes.
func TestTheDNSElementsOperationTakesDNSSetsOnly(t *testing.T) {
	if _, err := Decode([]byte(dnsOp(dnsSetA, dnsSetB))); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"another set":              dnsOp("ident4_aabbcc"),
		"the set of a rule":        dnsOp("asrc_0123456789ab_aabbcc"),
		"a short id":               dnsOp("dns_0123_abcdef"),
		"no hash":                  dnsOp("dns_0123456789"),
		"no update":                `{"type":"nft_dns_elements","updates":[]}`,
		"no element":               `{"type":"nft_dns_elements","updates":[{"set":"` + dnsSetA + `","elements":[]}]}`,
		"a prefix":                 `{"type":"nft_dns_elements","updates":[{"set":"` + dnsSetA + `","elements":[{"client":"10.10.0.0/24","addr":"203.0.113.7","timeout_seconds":600}]}]}`,
		"a name":                   `{"type":"nft_dns_elements","updates":[{"set":"` + dnsSetA + `","elements":[{"client":"10.10.0.1","addr":"example.com","timeout_seconds":600}]}]}`,
		"an IPv6 address":          `{"type":"nft_dns_elements","updates":[{"set":"` + dnsSetA + `","elements":[{"client":"10.10.0.1","addr":"2001:db8::1","timeout_seconds":600}]}]}`,
		"a multicast":              `{"type":"nft_dns_elements","updates":[{"set":"` + dnsSetA + `","elements":[{"client":"10.10.0.1","addr":"224.0.0.1","timeout_seconds":600}]}]}`,
		"no lifetime":              `{"type":"nft_dns_elements","updates":[{"set":"` + dnsSetA + `","elements":[{"client":"10.10.0.1","addr":"203.0.113.7","timeout_seconds":0}]}]}`,
		"a year":                   `{"type":"nft_dns_elements","updates":[{"set":"` + dnsSetA + `","elements":[{"client":"10.10.0.1","addr":"203.0.113.7","timeout_seconds":31536000}]}]}`,
		"an unknown field":         `{"type":"nft_dns_elements","updates":[{"set":"` + dnsSetA + `","elements":[{"client":"10.10.0.1","addr":"203.0.113.7","timeout_seconds":60,"x":1}]}]}`,
		"a namespace":              `{"type":"nft_dns_elements","namespace":"a b","updates":[{"set":"` + dnsSetA + `","elements":[{"client":"10.10.0.1","addr":"203.0.113.7","timeout_seconds":60}]}]}`,
		"a delete with a lifetime": `{"type":"nft_dns_elements","updates":[{"set":"` + dnsSetA + `","elements":[{"client":"10.10.0.1","addr":"203.0.113.7","timeout_seconds":60,"delete":true}]}]}`,
		"a delete that replaces":   `{"type":"nft_dns_elements","updates":[{"set":"` + dnsSetA + `","elements":[{"client":"10.10.0.1","addr":"203.0.113.7","replace":true,"delete":true}]}]}`,
	} {
		if _, err := Decode([]byte(body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if !IsDNSSetName(dnsSetA) || IsDNSSetName("dns_0123456789_abcdeX") {
		t.Error("IsDNSSetName")
	}
	// at most MaxDNSElements in all
	var els []string
	for i := 0; i <= MaxDNSElements; i++ {
		els = append(els, `{"client":"10.10.0.1","addr":"203.0.113.7","timeout_seconds":60}`)
	}
	if _, err := Decode([]byte(`{"type":"nft_dns_elements","updates":[{"set":"` + dnsSetA + `","elements":[` + strings.Join(els, ",") + `]}]}`)); err == nil {
		t.Error("too many elements accepted")
	}
}

// The elements are written as one nft transaction: a refresh deletes first, every add carries its lifetime, and the
// key is the pair of the client and the address.
func TestTheDNSElementsAreOneTransactionWithTheLifetimeOfEveryElement(t *testing.T) {
	fr := &fakeRunner{}
	e := newExec(t, fr)
	if _, err := e.Do(context.Background(), mustDecode(t, dnsOp(dnsSetA))); err != nil {
		t.Fatal(err)
	}
	cmds := fr.commands()
	if len(cmds) != 1 || cmds[0].Tool != ToolNft || strings.Join(cmds[0].Args, " ") != "-j -f -" {
		t.Fatalf("%+v", cmds)
	}
	var doc struct {
		Nftables []map[string]map[string]struct {
			Table string `json:"table"`
			Name  string `json:"name"`
			Elem  []any  `json:"elem"`
		} `json:"nftables"`
	}
	if err := json.Unmarshal([]byte(cmds[0].Stdin), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Nftables) != 2 {
		t.Fatalf("%s", cmds[0].Stdin)
	}
	del, add := doc.Nftables[0]["delete"]["element"], doc.Nftables[1]["add"]["element"]
	if del.Table != "chaosgw" || del.Name != dnsSetA || len(del.Elem) != 1 || add.Name != dnsSetA || len(add.Elem) != 2 {
		t.Errorf("%s", cmds[0].Stdin)
	}
	if !strings.Contains(cmds[0].Stdin, `{"elem":{"timeout":600,"val":{"concat":["10.10.0.42","203.0.113.7"]}}}`) {
		t.Errorf("the add does not carry the pair and the lifetime: %s", cmds[0].Stdin)
	}
	if !strings.Contains(cmds[0].Stdin, `"delete":{"element":{"elem":[{"concat":["10.10.0.43","203.0.113.8"]}]`) {
		t.Errorf("the refresh does not delete the element: %s", cmds[0].Stdin)
	}
}

// An element that was to be refreshed and ran out: the transaction with its delete fails, and the adds alone are written.
func TestARefreshOfAnElementThatRanOutIsAnAdd(t *testing.T) {
	var n atomic.Int32
	fr := &fakeRunner{respond: func(c Command) (Result, error) {
		if strings.Contains(c.Stdin, `"delete"`) {
			n.Add(1)
			return Result{Exit: 1, Stderr: "Error: Could not process rule: No such file or directory"}, nil
		}
		return Result{}, nil
	}}
	e := newExec(t, fr)
	if _, err := e.Do(context.Background(), mustDecode(t, dnsOp(dnsSetA))); err != nil {
		t.Fatal(err)
	}
	cmds := fr.commands()
	if n.Load() != 1 || len(cmds) != 2 || strings.Contains(cmds[1].Stdin, `"delete"`) || !strings.Contains(cmds[1].Stdin, `"add"`) {
		t.Errorf("%d commands: %+v", len(cmds), cmds)
	}
	// a failure of the adds is the request's failure
	fr2 := &fakeRunner{respond: func(Command) (Result, error) { return Result{Exit: 1, Stderr: "boom"}, nil }}
	e2 := newExec(t, fr2)
	if _, err := e2.Do(context.Background(), mustDecode(t, dnsOp(dnsSetA))); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("%v", err)
	}
}

// slowPlanRunner holds every plan (an nft command that is not an element update) until release is closed.
func slowPlanRunner() (fr *fakeRunner, started, release chan struct{}) {
	started, release = make(chan struct{}), make(chan struct{})
	var once sync.Once
	fr = &fakeRunner{respond: func(c Command) (Result, error) {
		if c.Tool == ToolNft && c.Stdin != "" && !strings.Contains(c.Stdin, `"element"`) {
			once.Do(func() { close(started) })
			<-release
		}
		return Result{Stdout: `[]`}, nil
	}}
	return fr, started, release
}

// Plan §3.11, M20: an update of a DNS-derived set does not wait for the plan that is running: on the executor and
// through a client over the socket, while the same client has a plan on its main connection.
func TestADNSSetUpdateIsWrittenWhileALongPlanRuns(t *testing.T) {
	fr, started, release := slowPlanRunner()
	path, e := startServer(t, fr, nil)
	c := dial(t, path)
	planDone := make(chan error, 1)
	go func() { _, err := e.Do(context.Background(), mustDecode(t, nftOp)); planDone <- err }()
	<-started
	clientPlan := make(chan error, 1)
	go func() { _, err := c.Do(context.Background(), mustDecode(t, nftOp)); clientPlan <- err }()
	for e.waiting() < 1 {
		time.Sleep(100 * time.Microsecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := e.Do(ctx, mustDecode(t, dnsOp(dnsSetA)))
	if err != nil || out.Completed != 1 || out.Started.Before(out.Enqueued) {
		t.Fatalf("an update beside the plan: %+v %v", out, err)
	}
	if _, err := c.Do(ctx, mustDecode(t, dnsOp(dnsSetB))); err != nil {
		t.Fatalf("an update through the client beside two plans: %v", err)
	}
	select {
	case err := <-planDone:
		t.Fatalf("the plan ended before it was released: %v", err)
	default:
	}
	var wrote int
	for _, cmd := range fr.commands() {
		if strings.Contains(cmd.Stdin, `"element"`) {
			wrote++
		}
	}
	if wrote < 2 {
		t.Errorf("%d element commands", wrote)
	}
	close(release)
	if err := <-planDone; err != nil {
		t.Fatal(err)
	}
	if err := <-clientPlan; err != nil {
		t.Fatal(err)
	}
}

// The updates are written one request at a time (the netlink connection has one user), whoever asks.
func TestDNSSetUpdatesAreWrittenOneRequestAtATime(t *testing.T) {
	var writing, overlap atomic.Int32
	fr := &fakeRunner{respond: func(c Command) (Result, error) {
		if writing.Add(1) > 1 {
			overlap.Add(1)
		}
		time.Sleep(time.Millisecond)
		writing.Add(-1)
		return Result{}, nil
	}}
	e := newExec(t, fr)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.Do(context.Background(), mustDecode(t, dnsOp(dnsSetA))); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if overlap.Load() != 0 || len(fr.commands()) != 40 {
		t.Errorf("%d overlaps, %d commands", overlap.Load(), len(fr.commands()))
	}
}

// A request that is cancelled while it waits for the update before it is not written; a closed executor refuses.
func TestADNSSetUpdateGivesUpWithItsContextAndAClosedExecutorRefuses(t *testing.T) {
	release := make(chan struct{})
	fr := &fakeRunner{respond: func(c Command) (Result, error) { <-release; return Result{}, nil }}
	e, err := New(fr)
	if err != nil {
		t.Fatal(err)
	}
	first := make(chan error, 1)
	go func() { _, err := e.Do(context.Background(), mustDecode(t, dnsOp(dnsSetA))); first <- err }()
	for len(fr.commands()) == 0 {
		time.Sleep(100 * time.Microsecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := e.Do(ctx, mustDecode(t, dnsOp(dnsSetB))); err == nil {
		t.Error("a request that waited for the update before it did not give up")
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	e.Close()
	if _, err := e.Do(context.Background(), mustDecode(t, dnsOp(dnsSetA))); err == nil {
		t.Error("a closed executor wrote an update")
	}
}

// With other operations in the request the update goes through the queue like everything else (as adds).
func TestADNSSetUpdateInAMixedRequestRunsInTheQueue(t *testing.T) {
	fr := &fakeRunner{}
	e := newExec(t, fr)
	if _, err := e.DoBatch(context.Background(), []Operation{mustDecode(t, nftOp), mustDecode(t, dnsOp(dnsSetA))}); err != nil {
		t.Fatal(err)
	}
	cmds := fr.commands()
	if len(cmds) != 2 || strings.Contains(cmds[1].Stdin, `"delete"`) || !strings.Contains(cmds[1].Stdin, dnsSetA) {
		t.Errorf("%+v", cmds)
	}
}

// The key of an element in the netlink message is the client's address and the answer's, four bytes each.
func TestTheNetlinkKeyOfAnElementIsTheTwoAddresses(t *testing.T) {
	k, err := elementKey(DNSElement{Client: "10.10.0.42", Addr: "203.0.113.7"})
	if err != nil || len(k) != 8 || k[0] != 10 || k[3] != 42 || k[4] != 203 || k[7] != 7 {
		t.Errorf("%v %v", k, err)
	}
	if _, err := elementKey(DNSElement{Client: "x", Addr: "203.0.113.7"}); err == nil {
		t.Error("a bad address")
	}
}

// Plan §2.3 (P5-AUDIT-01): an element can be taken out (the engine re-keys the elements of a device whose old address went
// to another device). The delete is in the transaction and has no add; when the transaction fails because an element is
// not there any more, the adds are written alone and every element to take out goes on its own, so one that ran out does
// not keep another in the set.
func TestADNSElementIsTakenOutAndOneThatRanOutDoesNotStopTheOthers(t *testing.T) {
	const op = `{"type":"nft_dns_elements","updates":[{"set":"` + dnsSetA + `","elements":[` +
		`{"client":"10.10.0.42","addr":"203.0.113.7","delete":true},{"client":"10.10.0.43","addr":"203.0.113.7","delete":true},` +
		`{"client":"10.10.0.44","addr":"203.0.113.7","timeout_seconds":540}]}]}`
	fr := &fakeRunner{}
	e := newExec(t, fr)
	if _, err := e.Do(context.Background(), mustDecode(t, op)); err != nil {
		t.Fatal(err)
	}
	cmds := fr.commands()
	if len(cmds) != 1 || !strings.Contains(cmds[0].Stdin, `"delete":{"element":{"elem":[{"concat":["10.10.0.42","203.0.113.7"]},{"concat":["10.10.0.43","203.0.113.7"]}]`) ||
		strings.Count(cmds[0].Stdin, `"timeout"`) != 1 || !strings.Contains(cmds[0].Stdin, `{"elem":{"timeout":540,"val":{"concat":["10.10.0.44","203.0.113.7"]}}}`) {
		t.Fatalf("%+v", cmds)
	}
	// 10.10.0.42 ran out: the transaction fails, the add goes alone, then each delete on its own
	fr2 := &fakeRunner{respond: func(c Command) (Result, error) {
		if strings.Contains(c.Stdin, `"10.10.0.42"`) && strings.Contains(c.Stdin, `"delete"`) {
			return Result{Exit: 1, Stderr: "Error: Could not process rule: No such file or directory"}, nil
		}
		return Result{}, nil
	}}
	e2 := newExec(t, fr2)
	if _, err := e2.Do(context.Background(), mustDecode(t, op)); err != nil {
		t.Fatal(err)
	}
	cmds = fr2.commands()
	if len(cmds) != 4 || strings.Contains(cmds[1].Stdin, `"delete"`) || !strings.Contains(cmds[1].Stdin, `"10.10.0.44"`) ||
		!strings.Contains(cmds[3].Stdin, `"delete":{"element":{"elem":[{"concat":["10.10.0.43","203.0.113.7"]}]`) || strings.Contains(cmds[3].Stdin, `"add"`) {
		t.Errorf("%d commands: %+v", len(cmds), cmds)
	}
}
