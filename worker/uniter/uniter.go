package uniter

import (
	"errors"
	"fmt"
	"launchpad.net/juju-core/cmd"
	"launchpad.net/juju-core/cmd/jujuc/server"
	"launchpad.net/juju-core/environs"
	"launchpad.net/juju-core/log"
	"launchpad.net/juju-core/state"
	"launchpad.net/juju-core/state/presence"
	"launchpad.net/juju-core/trivial"
	"launchpad.net/juju-core/worker/uniter/charm"
	"launchpad.net/juju-core/worker/uniter/hook"
	"launchpad.net/tomb"
	"math/rand"
	"path/filepath"
	"strings"
	"time"
)

// Uniter implements the capabilities of the unit agent. It is not intended to
// implement the actual *behaviour* of the unit agent; that responsibility is
// delegated to Mode values, which are expected to use the capabilities of the
// uniter to react appropriately to changes in the system.
type Uniter struct {
	tomb    tomb.Tomb
	path    string
	st      *state.State
	unit    *state.Unit
	service *state.Service
	hook    *hook.StateFile
	charm   *charm.Manager
	rand    *rand.Rand
	pinger  *presence.Pinger
}

// NewUniter creates a new Uniter which will install, run, and upgrade a
// charm on behalf of the named unit, by executing hooks and operations
// provoked by changes in st.
func NewUniter(st *state.State, name string) (u *Uniter, err error) {
	defer trivial.ErrorContextf(&err, "failed to create uniter for unit %q", name)
	path, err := ensureFs(name)
	if err != nil {
		return nil, err
	}
	unit, err := st.Unit(name)
	if err != nil {
		return nil, err
	}
	service, err := st.Service(unit.ServiceName())
	if err != nil {
		return nil, err
	}
	pinger, err := unit.SetAgentAlive()
	if err != nil {
		return nil, err
	}
	p := func(parts ...string) string {
		return filepath.Join(append([]string{path}, parts...)...)
	}
	u = &Uniter{
		path:    path,
		st:      st,
		unit:    unit,
		service: service,
		hook:    hook.NewStateFile(p("state", "hook")),
		charm:   charm.NewManager(p("charm"), p("state")),
		rand:    rand.New(rand.NewSource(time.Now().Unix())),
		pinger:  pinger,
	}
	go u.loop()
	return u, nil
}

func (u *Uniter) loop() {
	var err error
	mode := ModeInit
	for mode != nil {
		mode, err = mode(u)
	}
	select {
	case <-u.tomb.Dying():
	default:
		u.tomb.Kill(err)
	}
	u.tomb.Kill(u.pinger.Stop())
	u.tomb.Done()
}

func (u *Uniter) Stop() error {
	u.tomb.Kill(nil)
	return u.Wait()
}

func (u *Uniter) Dying() <-chan struct{} {
	return u.tomb.Dying()
}

func (u *Uniter) Wait() error {
	return u.tomb.Wait()
}

// changeCharm writes the supplied charm to the state directory. Before writing,
// it records the supplied charm and reason; when writing is complete, it sets
// the unit's charm in state. It does *not* clear the supplied charm and reason;
// to avoid sequence breaking, the change must only be marked complete once the
// associated hook has been marked as started.
func (u *Uniter) changeCharm(sch *state.Charm, st charm.Status) error {
	log.Printf("changing charm (%s)", st)
	if st != charm.Installing && st != charm.Upgrading {
		panic(fmt.Errorf("charm status %q does not represent a change", st))
	}
	if err := u.charm.WriteStatus(st, sch.URL()); err != nil {
		return err
	}
	if err := u.charm.Update(sch, &u.tomb); err != nil {
		return err
	}
	if err := u.unit.SetCharm(sch); err != nil {
		return err
	}
	log.Printf("charm changed successfully")
	return nil
}

// errHookFailed indicates that a hook failed to execute, but that the Uniter's
// operation is not affected by the error.
var errHookFailed = errors.New("hook execution failed")

// runHook executes the supplied hook.Info in an appropriate hook context. If
// the hook itself fails to execute, it returns errHookFailed.
func (u *Uniter) runHook(hi hook.Info) error {
	// Prepare context.
	hookName := string(hi.Kind)
	if hi.Kind.IsRelation() {
		panic("relation hooks are not yet supported")
		// TODO: update relation context; get hook name.
	}
	hctxId := fmt.Sprintf("%s:%s:%d", u.unit.Name(), hookName, u.rand.Int63())
	hctx := server.HookContext{
		Service:    u.service,
		Unit:       u.unit,
		Id:         hctxId,
		RelationId: -1,
	}
	getCmd := func(ctxId, cmdName string) (cmd.Command, error) {
		// TODO: switch to long-running server with single context;
		// use nonce in place of context id.
		if ctxId != hctxId {
			return nil, fmt.Errorf("expected context id %q, got %q", hctxId, ctxId)
		}
		return hctx.NewCommand(cmdName)
	}

	// Mark hook execution started.
	if err := u.hook.Write(hi, hook.Started); err != nil {
		return err
	}
	if hi.Kind == hook.Install || hi.Kind == hook.UpgradeCharm {
		// Once hook execution is started, we can forget about the charm
		// change operation; we'll restart from this point.
		if err := u.charm.WriteStatus(charm.Installed, nil); err != nil {
			return err
		}
	}
	socketPath := filepath.Join(u.path, "agent.socket")
	srv, err := server.NewServer(getCmd, socketPath)
	if err != nil {
		return err
	}
	go srv.Run()
	defer srv.Close()
	log.Printf("running hook %q", hookName)
	if err := hctx.RunHook(hookName, u.charm.Path(), socketPath); err != nil {
		log.Printf("hook failed: %s", err)
		return errHookFailed
	}
	if err := u.hook.Write(hi, hook.Succeeded); err != nil {
		return err
	}
	log.Printf("hook succeeded")
	return u.commitHook(hi)
}

// commitHook ensures that state is consistent with the supplied hook, and
// that the fact of the hook's completion is persisted.
func (u *Uniter) commitHook(hi hook.Info) error {
	if hi.Kind.IsRelation() {
		panic("relation hooks are not yet supported")
		// TODO: commit relation state changes.
	}
	if err := u.hook.Write(hi, hook.Committed); err != nil {
		return err
	}
	log.Printf("hook committed")
	return nil
}

// ensureFs ensures that files and directories required by the named uniter
// exist. It returns the path to the directory within which the uniter must
// store its data.
func ensureFs(name string) (string, error) {
	// TODO: do this OAOO at packaging time?
	if err := EnsureJujucSymlinks(name); err != nil {
		return "", err
	}
	path := filepath.Join(environs.VarDir, "units", strings.Replace(name, "/", "-", 1))
	if err := trivial.EnsureDir(filepath.Join(path, "state")); err != nil {
		return "", err
	}
	return path, nil
}
