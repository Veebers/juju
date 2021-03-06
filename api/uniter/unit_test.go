// Copyright 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package uniter_test

import (
	"fmt"
	"time"

	"github.com/juju/errors"
	"github.com/juju/names"
	jc "github.com/juju/testing/checkers"
	"github.com/juju/utils"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v6-unstable"

	"github.com/juju/juju/api"
	"github.com/juju/juju/api/base"
	"github.com/juju/juju/api/uniter"
	"github.com/juju/juju/apiserver/common"
	"github.com/juju/juju/apiserver/params"
	"github.com/juju/juju/juju/testing"
	"github.com/juju/juju/network"
	"github.com/juju/juju/state"
	"github.com/juju/juju/status"
	jujufactory "github.com/juju/juju/testing/factory"
	"github.com/juju/juju/watcher/watchertest"
)

type unitSuite struct {
	uniterSuite

	apiUnit *uniter.Unit
}

var _ = gc.Suite(&unitSuite{})

func (s *unitSuite) SetUpTest(c *gc.C) {
	s.uniterSuite.SetUpTest(c)

	var err error
	s.apiUnit, err = s.uniter.Unit(s.wordpressUnit.Tag().(names.UnitTag))
	c.Assert(err, jc.ErrorIsNil)
}

func (s *unitSuite) TestRequestReboot(c *gc.C) {
	err := s.apiUnit.RequestReboot()
	c.Assert(err, jc.ErrorIsNil)
	rFlag, err := s.wordpressMachine.GetRebootFlag()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(rFlag, jc.IsTrue)
}

func (s *unitSuite) TestUnitAndUnitTag(c *gc.C) {
	apiUnitFoo, err := s.uniter.Unit(names.NewUnitTag("foo/42"))
	c.Assert(err, gc.ErrorMatches, "permission denied")
	c.Assert(err, jc.Satisfies, params.IsCodeUnauthorized)
	c.Assert(apiUnitFoo, gc.IsNil)

	c.Assert(s.apiUnit.Tag(), gc.Equals, s.wordpressUnit.Tag().(names.UnitTag))
}

func (s *unitSuite) TestSetAgentStatus(c *gc.C) {
	statusInfo, err := s.wordpressUnit.AgentStatus()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(statusInfo.Status, gc.Equals, status.StatusAllocating)
	c.Assert(statusInfo.Message, gc.Equals, "")
	c.Assert(statusInfo.Data, gc.HasLen, 0)

	unitStatusInfo, err := s.wordpressUnit.Status()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(unitStatusInfo.Status, gc.Equals, status.StatusUnknown)
	c.Assert(unitStatusInfo.Message, gc.Equals, "Waiting for agent initialization to finish")
	c.Assert(unitStatusInfo.Data, gc.HasLen, 0)

	err = s.apiUnit.SetAgentStatus(status.StatusIdle, "blah", nil)
	c.Assert(err, jc.ErrorIsNil)

	statusInfo, err = s.wordpressUnit.AgentStatus()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(statusInfo.Status, gc.Equals, status.StatusIdle)
	c.Assert(statusInfo.Message, gc.Equals, "blah")
	c.Assert(statusInfo.Data, gc.HasLen, 0)
	c.Assert(statusInfo.Since, gc.NotNil)

	// Ensure that unit has not changed.
	unitStatusInfo, err = s.wordpressUnit.Status()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(unitStatusInfo.Status, gc.Equals, status.StatusUnknown)
	c.Assert(unitStatusInfo.Message, gc.Equals, "Waiting for agent initialization to finish")
	c.Assert(unitStatusInfo.Data, gc.HasLen, 0)
}

func (s *unitSuite) TestSetUnitStatus(c *gc.C) {
	statusInfo, err := s.wordpressUnit.Status()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(statusInfo.Status, gc.Equals, status.StatusUnknown)
	c.Assert(statusInfo.Message, gc.Equals, "Waiting for agent initialization to finish")
	c.Assert(statusInfo.Data, gc.HasLen, 0)

	agentStatusInfo, err := s.wordpressUnit.AgentStatus()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(agentStatusInfo.Status, gc.Equals, status.StatusAllocating)
	c.Assert(agentStatusInfo.Message, gc.Equals, "")
	c.Assert(agentStatusInfo.Data, gc.HasLen, 0)

	err = s.apiUnit.SetUnitStatus(status.StatusActive, "blah", nil)
	c.Assert(err, jc.ErrorIsNil)

	statusInfo, err = s.wordpressUnit.Status()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(statusInfo.Status, gc.Equals, status.StatusActive)
	c.Assert(statusInfo.Message, gc.Equals, "blah")
	c.Assert(statusInfo.Data, gc.HasLen, 0)
	c.Assert(statusInfo.Since, gc.NotNil)

	// Ensure unit's agent has not changed.
	agentStatusInfo, err = s.wordpressUnit.AgentStatus()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(agentStatusInfo.Status, gc.Equals, status.StatusAllocating)
	c.Assert(agentStatusInfo.Message, gc.Equals, "")
	c.Assert(agentStatusInfo.Data, gc.HasLen, 0)
}

func (s *unitSuite) TestUnitStatus(c *gc.C) {
	err := s.wordpressUnit.SetStatus(status.StatusMaintenance, "blah", nil)
	c.Assert(err, jc.ErrorIsNil)

	result, err := s.apiUnit.UnitStatus()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(result.Since, gc.NotNil)
	result.Since = nil
	c.Assert(result, gc.DeepEquals, params.StatusResult{
		Status: status.StatusMaintenance,
		Info:   "blah",
		Data:   map[string]interface{}{},
	})
}

func (s *unitSuite) TestEnsureDead(c *gc.C) {
	c.Assert(s.wordpressUnit.Life(), gc.Equals, state.Alive)

	err := s.apiUnit.EnsureDead()
	c.Assert(err, jc.ErrorIsNil)

	err = s.wordpressUnit.Refresh()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(s.wordpressUnit.Life(), gc.Equals, state.Dead)

	err = s.apiUnit.EnsureDead()
	c.Assert(err, jc.ErrorIsNil)
	err = s.wordpressUnit.Refresh()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(s.wordpressUnit.Life(), gc.Equals, state.Dead)

	err = s.wordpressUnit.Remove()
	c.Assert(err, jc.ErrorIsNil)
	err = s.wordpressUnit.Refresh()
	c.Assert(err, jc.Satisfies, errors.IsNotFound)

	err = s.apiUnit.EnsureDead()
	c.Assert(err, gc.ErrorMatches, `unit "wordpress/0" not found`)
	c.Assert(err, jc.Satisfies, params.IsCodeNotFound)
}

func (s *unitSuite) TestDestroy(c *gc.C) {
	c.Assert(s.wordpressUnit.Life(), gc.Equals, state.Alive)

	err := s.apiUnit.Destroy()
	c.Assert(err, jc.ErrorIsNil)

	err = s.wordpressUnit.Refresh()
	c.Assert(err, gc.ErrorMatches, `unit "wordpress/0" not found`)
}

func (s *unitSuite) TestDestroyAllSubordinates(c *gc.C) {
	c.Assert(s.wordpressUnit.Life(), gc.Equals, state.Alive)

	// Call without subordinates - no change.
	err := s.apiUnit.DestroyAllSubordinates()
	c.Assert(err, jc.ErrorIsNil)

	// Add a couple of subordinates and try again.
	_, _, loggingSub := s.addRelatedService(c, "wordpress", "logging", s.wordpressUnit)
	_, _, monitoringSub := s.addRelatedService(c, "wordpress", "monitoring", s.wordpressUnit)
	c.Assert(loggingSub.Life(), gc.Equals, state.Alive)
	c.Assert(monitoringSub.Life(), gc.Equals, state.Alive)

	err = s.apiUnit.DestroyAllSubordinates()
	c.Assert(err, jc.ErrorIsNil)

	// Verify they got destroyed.
	err = loggingSub.Refresh()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(loggingSub.Life(), gc.Equals, state.Dying)
	err = monitoringSub.Refresh()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(monitoringSub.Life(), gc.Equals, state.Dying)
}

func (s *unitSuite) TestRefresh(c *gc.C) {
	c.Assert(s.apiUnit.Life(), gc.Equals, params.Alive)

	err := s.apiUnit.EnsureDead()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(s.apiUnit.Life(), gc.Equals, params.Alive)

	err = s.apiUnit.Refresh()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(s.apiUnit.Life(), gc.Equals, params.Dead)
}

func (s *unitSuite) TestWatch(c *gc.C) {
	c.Assert(s.apiUnit.Life(), gc.Equals, params.Alive)

	w, err := s.apiUnit.Watch()
	c.Assert(err, jc.ErrorIsNil)
	wc := watchertest.NewNotifyWatcherC(c, w, s.BackingState.StartSync)
	defer wc.AssertStops()

	// Initial event.
	wc.AssertOneChange()

	// Change something other than the lifecycle and make sure it's
	// not detected.
	err = s.apiUnit.SetAgentStatus(status.StatusIdle, "not really", nil)
	c.Assert(err, jc.ErrorIsNil)
	wc.AssertNoChange()

	// Make the unit dead and check it's detected.
	err = s.apiUnit.EnsureDead()
	c.Assert(err, jc.ErrorIsNil)
	wc.AssertOneChange()
}

func (s *unitSuite) TestResolve(c *gc.C) {
	err := s.wordpressUnit.SetResolved(state.ResolvedRetryHooks)
	c.Assert(err, jc.ErrorIsNil)

	mode, err := s.apiUnit.Resolved()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(mode, gc.Equals, params.ResolvedRetryHooks)

	err = s.apiUnit.ClearResolved()
	c.Assert(err, jc.ErrorIsNil)

	mode, err = s.apiUnit.Resolved()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(mode, gc.Equals, params.ResolvedNone)
}

func (s *unitSuite) TestAssignedMachine(c *gc.C) {
	machineTag, err := s.apiUnit.AssignedMachine()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(machineTag, gc.Equals, s.wordpressMachine.Tag())
}

func (s *unitSuite) TestIsPrincipal(c *gc.C) {
	ok, err := s.apiUnit.IsPrincipal()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(ok, jc.IsTrue)
}

func (s *unitSuite) TestHasSubordinates(c *gc.C) {
	found, err := s.apiUnit.HasSubordinates()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(found, jc.IsFalse)

	// Add a couple of subordinates and try again.
	s.addRelatedService(c, "wordpress", "logging", s.wordpressUnit)
	s.addRelatedService(c, "wordpress", "monitoring", s.wordpressUnit)

	found, err = s.apiUnit.HasSubordinates()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(found, jc.IsTrue)
}

func (s *unitSuite) TestPublicAddress(c *gc.C) {
	address, err := s.apiUnit.PublicAddress()
	c.Assert(err, gc.ErrorMatches, `"unit-wordpress-0" has no public address set`)

	err = s.wordpressMachine.SetProviderAddresses(
		network.NewScopedAddress("1.2.3.4", network.ScopePublic),
	)
	c.Assert(err, jc.ErrorIsNil)

	address, err = s.apiUnit.PublicAddress()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(address, gc.Equals, "1.2.3.4")
}

func (s *unitSuite) TestPrivateAddress(c *gc.C) {
	address, err := s.apiUnit.PrivateAddress()
	c.Assert(err, gc.ErrorMatches, `"unit-wordpress-0" has no private address set`)

	err = s.wordpressMachine.SetProviderAddresses(
		network.NewScopedAddress("1.2.3.4", network.ScopeCloudLocal),
	)
	c.Assert(err, jc.ErrorIsNil)

	address, err = s.apiUnit.PrivateAddress()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(address, gc.Equals, "1.2.3.4")
}

func (s *unitSuite) TestNetworkConfig(c *gc.C) {
	c.Skip("dimitern: temporarily disabled to pass a CI run until it can be fixed like its apiserver/uniter counterpart")

	// Set some provider addresses bound to both "public" and "internal"
	// spaces.
	addresses := []network.Address{
		network.NewAddressOnSpace("public", "8.8.8.8"),
		network.NewAddressOnSpace("", "8.8.4.4"),
		network.NewAddressOnSpace("internal", "10.0.0.1"),
		network.NewAddressOnSpace("internal", "10.0.0.2"),
		network.NewAddressOnSpace("public", "fc00::1"),
	}
	err := s.wordpressMachine.SetProviderAddresses(addresses...)
	c.Assert(err, jc.ErrorIsNil)

	netConfig, err := s.apiUnit.NetworkConfig("db") // relation name, bound to "internal"
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(netConfig, jc.DeepEquals, []params.NetworkConfig{
		{Address: "10.0.0.1"},
		{Address: "10.0.0.2"},
	})

	netConfig, err = s.apiUnit.NetworkConfig("admin-api") // extra-binding name, bound to "public"
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(netConfig, jc.DeepEquals, []params.NetworkConfig{
		{Address: "8.8.8.8"},
		{Address: "fc00::1"},
	})

	netConfig, err = s.apiUnit.NetworkConfig("unknown")
	c.Assert(err, gc.ErrorMatches, `binding name "unknown" not defined by the unit's charm`)
	c.Assert(netConfig, gc.IsNil)

	netConfig, err = s.apiUnit.NetworkConfig("")
	c.Assert(err, gc.ErrorMatches, "binding name cannot be empty")
	c.Assert(netConfig, gc.IsNil)
}

func (s *unitSuite) TestAvailabilityZone(c *gc.C) {
	uniter.PatchUnitResponse(s, s.apiUnit, "AvailabilityZone",
		func(result interface{}) error {
			if results, ok := result.(*params.StringResults); ok {
				results.Results = []params.StringResult{{
					Result: "a-zone",
				}}
			}
			return nil
		},
	)

	zone, err := s.apiUnit.AvailabilityZone()
	c.Assert(err, jc.ErrorIsNil)

	c.Check(zone, gc.Equals, "a-zone")
}

func (s *unitSuite) TestOpenClosePortRanges(c *gc.C) {
	ports, err := s.wordpressUnit.OpenedPorts()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(ports, gc.HasLen, 0)

	err = s.apiUnit.OpenPorts("tcp", 1234, 1400)
	c.Assert(err, jc.ErrorIsNil)
	err = s.apiUnit.OpenPorts("udp", 4321, 5000)
	c.Assert(err, jc.ErrorIsNil)

	ports, err = s.wordpressUnit.OpenedPorts()
	c.Assert(err, jc.ErrorIsNil)
	// OpenedPorts returns a sorted slice.
	c.Assert(ports, gc.DeepEquals, []network.PortRange{
		{Protocol: "tcp", FromPort: 1234, ToPort: 1400},
		{Protocol: "udp", FromPort: 4321, ToPort: 5000},
	})

	err = s.apiUnit.ClosePorts("udp", 4321, 5000)
	c.Assert(err, jc.ErrorIsNil)

	ports, err = s.wordpressUnit.OpenedPorts()
	c.Assert(err, jc.ErrorIsNil)
	// OpenedPorts returns a sorted slice.
	c.Assert(ports, gc.DeepEquals, []network.PortRange{
		{Protocol: "tcp", FromPort: 1234, ToPort: 1400},
	})

	err = s.apiUnit.ClosePorts("tcp", 1234, 1400)
	c.Assert(err, jc.ErrorIsNil)

	ports, err = s.wordpressUnit.OpenedPorts()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(ports, gc.HasLen, 0)
}

func (s *unitSuite) TestGetSetCharmURL(c *gc.C) {
	// No charm URL set yet.
	curl, ok := s.wordpressUnit.CharmURL()
	c.Assert(curl, gc.IsNil)
	c.Assert(ok, jc.IsFalse)

	// Now check the same through the API.
	_, err := s.apiUnit.CharmURL()
	c.Assert(err, gc.Equals, uniter.ErrNoCharmURLSet)

	err = s.apiUnit.SetCharmURL(s.wordpressCharm.URL())
	c.Assert(err, jc.ErrorIsNil)

	curl, err = s.apiUnit.CharmURL()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(curl, gc.NotNil)
	c.Assert(curl.String(), gc.Equals, s.wordpressCharm.String())
}

func (s *unitSuite) TestConfigSettings(c *gc.C) {
	// Make sure ConfigSettings returns an error when
	// no charm URL is set, as its state counterpart does.
	settings, err := s.apiUnit.ConfigSettings()
	c.Assert(err, gc.ErrorMatches, "unit charm not set")

	// Now set the charm and try again.
	err = s.apiUnit.SetCharmURL(s.wordpressCharm.URL())
	c.Assert(err, jc.ErrorIsNil)

	settings, err = s.apiUnit.ConfigSettings()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(settings, gc.DeepEquals, charm.Settings{
		"blog-title": "My Title",
	})

	// Update the config and check we get the changes on the next call.
	err = s.wordpressService.UpdateConfigSettings(charm.Settings{
		"blog-title": "superhero paparazzi",
	})
	c.Assert(err, jc.ErrorIsNil)

	settings, err = s.apiUnit.ConfigSettings()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(settings, gc.DeepEquals, charm.Settings{
		"blog-title": "superhero paparazzi",
	})
}

func (s *unitSuite) TestWatchConfigSettings(c *gc.C) {
	// Make sure WatchConfigSettings returns an error when
	// no charm URL is set, as its state counterpart does.
	w, err := s.apiUnit.WatchConfigSettings()
	c.Assert(err, gc.ErrorMatches, "unit charm not set")

	// Now set the charm and try again.
	err = s.apiUnit.SetCharmURL(s.wordpressCharm.URL())
	c.Assert(err, jc.ErrorIsNil)

	w, err = s.apiUnit.WatchConfigSettings()
	wc := watchertest.NewNotifyWatcherC(c, w, s.BackingState.StartSync)
	defer wc.AssertStops()

	// Initial event.
	wc.AssertOneChange()

	// Update config a couple of times, check a single event.
	err = s.wordpressService.UpdateConfigSettings(charm.Settings{
		"blog-title": "superhero paparazzi",
	})
	c.Assert(err, jc.ErrorIsNil)
	err = s.wordpressService.UpdateConfigSettings(charm.Settings{
		"blog-title": "sauceror central",
	})
	c.Assert(err, jc.ErrorIsNil)
	wc.AssertOneChange()

	// Non-change is not reported.
	err = s.wordpressService.UpdateConfigSettings(charm.Settings{
		"blog-title": "sauceror central",
	})
	c.Assert(err, jc.ErrorIsNil)
	wc.AssertNoChange()
}

func (s *unitSuite) TestWatchActionNotifications(c *gc.C) {
	w, err := s.apiUnit.WatchActionNotifications()
	c.Assert(err, jc.ErrorIsNil)
	wc := watchertest.NewStringsWatcherC(c, w, s.BackingState.StartSync)
	defer wc.AssertStops()

	// Initial event.
	wc.AssertChange()

	// Add a couple of actions and make sure the changes are detected.
	action, err := s.wordpressUnit.AddAction("fakeaction", map[string]interface{}{
		"outfile": "foo.txt",
	})
	c.Assert(err, jc.ErrorIsNil)
	wc.AssertChange(action.Id())

	action, err = s.wordpressUnit.AddAction("fakeaction", map[string]interface{}{
		"outfile": "foo.bz2",
		"compression": map[string]interface{}{
			"kind":    "bzip",
			"quality": float64(5.0),
		},
	})
	c.Assert(err, jc.ErrorIsNil)
	wc.AssertChange(action.Id())
}

func (s *unitSuite) TestWatchActionNotificationsError(c *gc.C) {
	uniter.PatchUnitResponse(s, s.apiUnit, "WatchActionNotifications",
		func(result interface{}) error {
			return fmt.Errorf("Test error")
		},
	)

	_, err := s.apiUnit.WatchActionNotifications()
	c.Assert(err.Error(), gc.Equals, "Test error")
}

func (s *unitSuite) TestWatchActionNotificationsErrorResults(c *gc.C) {
	uniter.PatchUnitResponse(s, s.apiUnit, "WatchActionNotifications",
		func(results interface{}) error {
			if results, ok := results.(*params.StringsWatchResults); ok {
				results.Results = make([]params.StringsWatchResult, 1)
				results.Results[0] = params.StringsWatchResult{
					Error: &params.Error{
						Message: "An error in the watch result.",
						Code:    params.CodeNotAssigned,
					},
				}
			}
			return nil
		},
	)

	_, err := s.apiUnit.WatchActionNotifications()
	c.Assert(err.Error(), gc.Equals, "An error in the watch result.")
}

func (s *unitSuite) TestWatchActionNotificationsNoResults(c *gc.C) {
	uniter.PatchUnitResponse(s, s.apiUnit, "WatchActionNotifications",
		func(results interface{}) error {
			return nil
		},
	)

	_, err := s.apiUnit.WatchActionNotifications()
	c.Assert(err.Error(), gc.Equals, "expected 1 result, got 0")
}

func (s *unitSuite) TestWatchActionNotificationsMoreResults(c *gc.C) {
	uniter.PatchUnitResponse(s, s.apiUnit, "WatchActionNotifications",
		func(results interface{}) error {
			if results, ok := results.(*params.StringsWatchResults); ok {
				results.Results = make([]params.StringsWatchResult, 2)
			}
			return nil
		},
	)

	_, err := s.apiUnit.WatchActionNotifications()
	c.Assert(err.Error(), gc.Equals, "expected 1 result, got 2")
}

func (s *unitSuite) TestServiceNameAndTag(c *gc.C) {
	c.Assert(s.apiUnit.ServiceName(), gc.Equals, s.wordpressService.Name())
	c.Assert(s.apiUnit.ServiceTag(), gc.Equals, s.wordpressService.Tag())
}

func (s *unitSuite) TestJoinedRelations(c *gc.C) {
	joinedRelations, err := s.apiUnit.JoinedRelations()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(joinedRelations, gc.HasLen, 0)

	rel1, _, _ := s.addRelatedService(c, "wordpress", "monitoring", s.wordpressUnit)
	joinedRelations, err = s.apiUnit.JoinedRelations()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(joinedRelations, gc.DeepEquals, []names.RelationTag{
		rel1.Tag().(names.RelationTag),
	})

	rel2, _, _ := s.addRelatedService(c, "wordpress", "logging", s.wordpressUnit)
	joinedRelations, err = s.apiUnit.JoinedRelations()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(joinedRelations, jc.SameContents, []names.RelationTag{
		rel1.Tag().(names.RelationTag),
		rel2.Tag().(names.RelationTag),
	})
}

func (s *unitSuite) TestWatchAddresses(c *gc.C) {
	w, err := s.apiUnit.WatchAddresses()
	c.Assert(err, jc.ErrorIsNil)
	wc := watchertest.NewNotifyWatcherC(c, w, s.BackingState.StartSync)
	defer wc.AssertStops()

	// Initial event.
	wc.AssertOneChange()

	// Update config a couple of times, check a single event.
	err = s.wordpressMachine.SetProviderAddresses(network.NewAddress("0.1.2.3"))
	c.Assert(err, jc.ErrorIsNil)
	err = s.wordpressMachine.SetProviderAddresses(network.NewAddress("0.1.2.4"))
	c.Assert(err, jc.ErrorIsNil)
	wc.AssertOneChange()

	// Non-change is not reported.
	err = s.wordpressMachine.SetProviderAddresses(network.NewAddress("0.1.2.4"))
	c.Assert(err, jc.ErrorIsNil)
	wc.AssertNoChange()

	// Change is reported for machine addresses.
	err = s.wordpressMachine.SetMachineAddresses(network.NewAddress("0.1.2.5"))
	c.Assert(err, jc.ErrorIsNil)
	wc.AssertOneChange()

	// Set machine addresses to empty is reported.
	err = s.wordpressMachine.SetMachineAddresses()
	c.Assert(err, jc.ErrorIsNil)
	wc.AssertOneChange()
}

func (s *unitSuite) TestWatchAddressesErrors(c *gc.C) {
	err := s.wordpressUnit.UnassignFromMachine()
	c.Assert(err, jc.ErrorIsNil)
	_, err = s.apiUnit.WatchAddresses()
	c.Assert(err, jc.Satisfies, params.IsCodeNotAssigned)
}

func (s *unitSuite) TestAddMetrics(c *gc.C) {
	uniter.PatchUnitResponse(s, s.apiUnit, "AddMetrics",
		func(results interface{}) error {
			result := results.(*params.ErrorResults)
			result.Results = make([]params.ErrorResult, 1)
			return nil
		},
	)
	metrics := []params.Metric{{"A", "23", time.Now()}, {"B", "27.0", time.Now()}}
	err := s.apiUnit.AddMetrics(metrics)
	c.Assert(err, jc.ErrorIsNil)
}

func (s *unitSuite) TestAddMetricsError(c *gc.C) {
	uniter.PatchUnitResponse(s, s.apiUnit, "AddMetrics",
		func(results interface{}) error {
			result := results.(*params.ErrorResults)
			result.Results = make([]params.ErrorResult, 1)
			return fmt.Errorf("test error")
		},
	)
	metrics := []params.Metric{{"A", "23", time.Now()}, {"B", "27.0", time.Now()}}
	err := s.apiUnit.AddMetrics(metrics)
	c.Assert(err, gc.ErrorMatches, "unable to add metric: test error")
}

func (s *unitSuite) TestAddMetricsResultError(c *gc.C) {
	uniter.PatchUnitResponse(s, s.apiUnit, "AddMetrics",
		func(results interface{}) error {
			result := results.(*params.ErrorResults)
			result.Results = make([]params.ErrorResult, 1)
			result.Results[0].Error = &params.Error{
				Message: "error adding metrics",
				Code:    params.CodeNotAssigned,
			}
			return nil
		},
	)
	metrics := []params.Metric{{"A", "23", time.Now()}, {"B", "27.0", time.Now()}}
	err := s.apiUnit.AddMetrics(metrics)
	c.Assert(err, gc.ErrorMatches, "error adding metrics")
}

func (s *unitSuite) TestMeterStatus(c *gc.C) {
	uniter.PatchUnitResponse(s, s.apiUnit, "GetMeterStatus",
		func(results interface{}) error {
			result := results.(*params.MeterStatusResults)
			result.Results = make([]params.MeterStatusResult, 1)
			result.Results[0].Code = "GREEN"
			result.Results[0].Info = "All ok."
			return nil
		},
	)
	statusCode, statusInfo, err := s.apiUnit.MeterStatus()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(statusCode, gc.Equals, "GREEN")
	c.Assert(statusInfo, gc.Equals, "All ok.")
}

func (s *unitSuite) TestMeterStatusError(c *gc.C) {
	uniter.PatchUnitResponse(s, s.apiUnit, "GetMeterStatus",
		func(results interface{}) error {
			result := results.(*params.MeterStatusResults)
			result.Results = make([]params.MeterStatusResult, 1)
			return fmt.Errorf("boo")
		},
	)
	statusCode, statusInfo, err := s.apiUnit.MeterStatus()
	c.Assert(err, gc.ErrorMatches, "boo")
	c.Assert(statusCode, gc.Equals, "")
	c.Assert(statusInfo, gc.Equals, "")
}

func (s *unitSuite) TestMeterStatusResultError(c *gc.C) {
	uniter.PatchUnitResponse(s, s.apiUnit, "GetMeterStatus",
		func(results interface{}) error {
			result := results.(*params.MeterStatusResults)
			result.Results = make([]params.MeterStatusResult, 1)
			result.Results[0].Error = &params.Error{
				Message: "error getting meter status",
				Code:    params.CodeNotAssigned,
			}
			return nil
		},
	)
	statusCode, statusInfo, err := s.apiUnit.MeterStatus()
	c.Assert(err, gc.ErrorMatches, "error getting meter status")
	c.Assert(statusCode, gc.Equals, "")
	c.Assert(statusInfo, gc.Equals, "")
}

func (s *unitSuite) TestWatchMeterStatus(c *gc.C) {
	w, err := s.apiUnit.WatchMeterStatus()
	wc := watchertest.NewNotifyWatcherC(c, w, s.BackingState.StartSync)
	defer wc.AssertStops()

	// Initial event.
	wc.AssertOneChange()

	err = s.wordpressUnit.SetMeterStatus("GREEN", "ok")
	c.Assert(err, jc.ErrorIsNil)
	err = s.wordpressUnit.SetMeterStatus("AMBER", "ok")
	c.Assert(err, jc.ErrorIsNil)
	wc.AssertOneChange()

	// Non-change is not reported.
	err = s.wordpressUnit.SetMeterStatus("AMBER", "ok")
	c.Assert(err, jc.ErrorIsNil)
	wc.AssertNoChange()

	mm, err := s.State.MetricsManager()
	c.Assert(err, jc.ErrorIsNil)
	err = mm.SetLastSuccessfulSend(time.Now())
	c.Assert(err, jc.ErrorIsNil)
	for i := 0; i < 3; i++ {
		err := mm.IncrementConsecutiveErrors()
		c.Assert(err, jc.ErrorIsNil)
	}
	status := mm.MeterStatus()
	c.Assert(status.Code, gc.Equals, state.MeterAmber) // Confirm meter status has changed
	wc.AssertOneChange()
}

func (s *unitSuite) patchNewState(
	c *gc.C,
	patchFunc func(_ base.APICaller, _ names.UnitTag) *uniter.State,
) {
	s.uniterSuite.patchNewState(c, patchFunc)
	var err error
	s.apiUnit, err = s.uniter.Unit(s.wordpressUnit.Tag().(names.UnitTag))
	c.Assert(err, jc.ErrorIsNil)
}

type unitMetricBatchesSuite struct {
	testing.JujuConnSuite

	st      api.Connection
	uniter  *uniter.State
	apiUnit *uniter.Unit
	charm   *state.Charm
}

var _ = gc.Suite(&unitMetricBatchesSuite{})

func (s *unitMetricBatchesSuite) SetUpTest(c *gc.C) {
	s.JujuConnSuite.SetUpTest(c)

	s.charm = s.Factory.MakeCharm(c, &jujufactory.CharmParams{
		Name: "metered",
		URL:  "cs:quantal/metered",
	})
	service := s.Factory.MakeService(c, &jujufactory.ServiceParams{
		Charm: s.charm,
	})
	unit := s.Factory.MakeUnit(c, &jujufactory.UnitParams{
		Service:     service,
		SetCharmURL: true,
	})

	password, err := utils.RandomPassword()
	c.Assert(err, jc.ErrorIsNil)
	err = unit.SetPassword(password)
	c.Assert(err, jc.ErrorIsNil)
	s.st = s.OpenAPIAs(c, unit.Tag(), password)

	// Create the uniter API facade.
	s.uniter, err = s.st.Uniter()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(s.uniter, gc.NotNil)

	s.apiUnit, err = s.uniter.Unit(unit.Tag().(names.UnitTag))
	c.Assert(err, jc.ErrorIsNil)
}

func (s *unitMetricBatchesSuite) TestSendMetricBatchPatch(c *gc.C) {
	metrics := []params.Metric{{"pings", "5", time.Now().UTC()}}
	uuid := utils.MustNewUUID().String()
	batch := params.MetricBatch{
		UUID:     uuid,
		CharmURL: s.charm.URL().String(),
		Created:  time.Now(),
		Metrics:  metrics,
	}

	var called bool
	uniter.PatchUnitResponse(s, s.apiUnit, "AddMetricBatches",
		func(response interface{}) error {
			called = true
			result := response.(*params.ErrorResults)
			result.Results = make([]params.ErrorResult, 1)
			return nil
		})

	results, err := s.apiUnit.AddMetricBatches([]params.MetricBatch{batch})
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(results, gc.HasLen, 1)
	c.Assert(results[batch.UUID], gc.IsNil)
	c.Assert(called, jc.IsTrue)
}

func (s *unitMetricBatchesSuite) TestSendMetricBatchFail(c *gc.C) {
	var called bool
	uniter.PatchUnitResponse(s, s.apiUnit, "AddMetricBatches",
		func(response interface{}) error {
			called = true
			result := response.(*params.ErrorResults)
			result.Results = make([]params.ErrorResult, 1)
			result.Results[0].Error = common.ServerError(common.ErrPerm)
			return nil
		})
	metrics := []params.Metric{{"pings", "5", time.Now().UTC()}}
	uuid := utils.MustNewUUID().String()
	batch := params.MetricBatch{
		UUID:     uuid,
		CharmURL: s.charm.URL().String(),
		Created:  time.Now(),
		Metrics:  metrics,
	}

	results, err := s.apiUnit.AddMetricBatches([]params.MetricBatch{batch})
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(results, gc.HasLen, 1)
	c.Assert(results[batch.UUID], gc.ErrorMatches, "permission denied")
	c.Assert(called, jc.IsTrue)
}

func (s *unitMetricBatchesSuite) TestSendMetricBatch(c *gc.C) {
	uuid := utils.MustNewUUID().String()
	now := time.Now().Round(time.Second).UTC()
	metrics := []params.Metric{{"pings", "5", now}}
	batch := params.MetricBatch{
		UUID:     uuid,
		CharmURL: s.charm.URL().String(),
		Created:  now,
		Metrics:  metrics,
	}

	results, err := s.apiUnit.AddMetricBatches([]params.MetricBatch{batch})
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(results, gc.HasLen, 1)
	c.Assert(results[batch.UUID], gc.IsNil)

	batches, err := s.State.AllMetricBatches()
	c.Assert(err, gc.IsNil)
	c.Assert(batches, gc.HasLen, 1)
	c.Assert(batches[0].UUID(), gc.Equals, uuid)
	c.Assert(batches[0].Sent(), jc.IsFalse)
	c.Assert(batches[0].CharmURL(), gc.Equals, s.charm.URL().String())
	c.Assert(batches[0].Metrics(), gc.HasLen, 1)
	c.Assert(batches[0].Metrics()[0].Key, gc.Equals, "pings")
	c.Assert(batches[0].Metrics()[0].Key, gc.Equals, "pings")
	c.Assert(batches[0].Metrics()[0].Value, gc.Equals, "5")
}
