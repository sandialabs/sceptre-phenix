package scorch

import (
	"context"
	"fmt"
	"os"
	"strings"

	"phenix/internal/mm"
	"phenix/util"

	log "github.com/activeshadow/libminimega/minilog"
	"github.com/mitchellh/mapstructure"
)

type TapMetadata struct {
	Bridge   string `mapstructure:"bridge"`
	VLAN     string `mapstructure:"vlan"`
	IP       string `mapstructure:"ip"`
	Internet bool   `mapstructure:"internetAccess"`
}

func (this *TapMetadata) Validate() error {
	if this == nil {
		return nil
	}

	if this.VLAN == "" {
		return fmt.Errorf("tap VLAN not specified")
	}

	if this.IP == "" {
		return fmt.Errorf("tap IP not specified")
	}

	if this.Bridge == "" {
		this.Bridge = "phenix"
	}

	return nil
}

type Tap struct {
	options Options
}

func (this *Tap) Init(opts ...Option) error {
	this.options = NewOptions(opts...)
	return nil
}

func (Tap) Type() string {
	return "tap"
}

func (Tap) Configure(context.Context) error {
	return nil
}

func (this Tap) Start(ctx context.Context) error {
	exp := this.options.Exp.Spec.ExperimentName()

	var md TapMetadata

	if err := mapstructure.Decode(this.options.Meta, &md); err != nil {
		return fmt.Errorf("decoding tap component metadata: %w", err)
	}

	if err := md.Validate(); err != nil {
		return fmt.Errorf("validating tap component metadata: %w", err)
	}

	// tap names cannot be longer than 15 characters
	// (dictated by max length of Linux interface names)
	tapName := fmt.Sprintf("tap_%s", util.RandomString(11))

	///// BEGIN PERSISTING TAP NAME TO STORE \\\\\
	var scorchStatus map[string]interface{}

	if status, ok := this.options.Exp.Status.AppStatus()["scorch"]; ok {
		scorchStatus = status.(map[string]interface{})
	} else {
		scorchStatus = make(map[string]interface{})
	}

	var tapStatus map[string]string

	if status, ok := scorchStatus["tap"]; ok {
		tapStatus = status.(map[string]string)
	} else {
		tapStatus = make(map[string]string)
	}

	tapStatus[this.options.Name] = tapName
	scorchStatus["tap"] = tapStatus

	this.options.Exp.Status.SetAppStatus("scorch", scorchStatus)
	this.options.Exp.WriteToStore(true)
	///// END PERSISTING TAP NAME TO STORE \\\\\

	routed, err := getDefaultInterface()
	if err != nil {
		return fmt.Errorf("getting interface for default route: %w", err)
	}

	if err := setupTap(md, exp, tapName, routed); err != nil {
		return fmt.Errorf("setting up tap: %w", err)
	}

	return nil
}

func (this Tap) Stop(ctx context.Context) error {
	exp := this.options.Exp.Spec.ExperimentName()

	var md TapMetadata

	if err := mapstructure.Decode(this.options.Meta, &md); err != nil {
		return fmt.Errorf("decoding tap component metadata: %w", err)
	}

	if err := md.Validate(); err != nil {
		return fmt.Errorf("validating tap component metadata: %w", err)
	}

	///// BEGIN GETTING TAP NAME FROM STORE \\\\\
	var scorchStatus map[string]interface{}

	if status, ok := this.options.Exp.Status.AppStatus()["scorch"]; ok {
		scorchStatus = status.(map[string]interface{})
	} else {
		return fmt.Errorf("tap name for tap component %s missing from experiment status", this.options.Name)
	}

	var tapStatus map[string]string

	if status, ok := scorchStatus["tap"]; ok {
		tapStatus = status.(map[string]string)
	} else {
		return fmt.Errorf("tap name for tap component %s missing from experiment status", this.options.Name)
	}

	tapName := tapStatus[this.options.Name]
	delete(tapStatus, this.options.Name)

	if len(tapStatus) == 0 {
		delete(scorchStatus, "tap")
	} else {
		scorchStatus["tap"] = tapStatus
	}

	this.options.Exp.Status.SetAppStatus("scorch", scorchStatus)
	this.options.Exp.WriteToStore(true)
	///// END GETTING TAP NAME FROM STORE \\\\\

	routed, err := getDefaultInterface()
	if err != nil {
		return fmt.Errorf("getting interface for default route: %w", err)
	}

	if err := teardownTap(md, exp, tapName, routed); err != nil {
		return fmt.Errorf("tearing down tap: %w", err)
	}

	return nil
}

func (Tap) Cleanup(context.Context) error {
	return nil
}

func setupTap(md TapMetadata, exp, tapName, routed string) error {
	opts := []mm.TapOption{
		mm.TapNS(exp),
		mm.TapBridge(md.Bridge),
		mm.TapVLANAlias(md.VLAN),
		mm.TapIP(md.IP),
		mm.TapName(tapName),
	}

	if err := mm.TapVLAN(opts...); err != nil {
		return fmt.Errorf(
			"tapping VLAN %s on bridge %s for experiment %s: %w",
			md.VLAN, md.Bridge, exp, err,
		)
	}

	if md.Internet {
		for _, rule := range getIPTablesFilterRules(tapName, routed) {
			log.Debug("APPEND FILTER RULE: " + rule)

			// TODO: check for existence with -C first

			cmd := fmt.Sprintf("iptables -t filter -A FORWARD %s", rule)

			// create rule on the minimega host
			if err := mm.Shell(cmd); err != nil {
				return fmt.Errorf("appending iptables filter rule: %w", err)
			}
		}

		for _, rule := range getIPTablesNATRules(routed) {
			log.Debug("APPEND NAT RULE: " + rule)

			// TODO: check for existence with -C first

			cmd := fmt.Sprintf("iptables -t nat -A POSTROUTING %s", rule)

			// create rule on the minimega host
			if err := mm.Shell(cmd); err != nil {
				return fmt.Errorf("appending iptables nat rule: %w", err)
			}
		}
	}

	return nil
}

func teardownTap(md TapMetadata, exp, tapName, routed string) error {
	if md.Internet {
		for _, rule := range getIPTablesNATRules(routed) {
			log.Debug("DELETE NAT RULE: " + rule)

			cmd := fmt.Sprintf("iptables -t nat -D POSTROUTING %s", rule)

			if err := mm.Shell(cmd); err != nil {
				return fmt.Errorf("deleting iptables nat rule: %w", err)
			}
		}

		for _, rule := range getIPTablesFilterRules(tapName, routed) {
			log.Debug("DELETE FILTER RULE: " + rule)

			cmd := fmt.Sprintf("iptables -t filter -D FORWARD %s", rule)

			if err := mm.Shell(cmd); err != nil {
				return fmt.Errorf("deleting iptables filter rule: %w", err)
			}
		}
	}

	opts := []mm.TapOption{
		mm.TapNS(exp),
		mm.TapName(tapName),
		mm.TapDelete(),
	}

	if err := mm.TapVLAN(opts...); err != nil {
		return fmt.Errorf(
			"untapping VLAN %s on bridge %s for experiment %s: %w",
			md.VLAN, md.Bridge, exp, err,
		)
	}

	return nil
}

func getDefaultInterface() (string, error) {
	data, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return "", fmt.Errorf("unable to read '/proc/net/route': %w", err)
	}

	lines := strings.Split(string(data), "\n")
	fields := strings.Fields(lines[1])

	return fields[0], nil
}

func getIPTablesFilterRules(tapName, routed string) []string {
	return []string{
		fmt.Sprintf(
			"-i %s -o %s -m conntrack --ctstate NEW -j ACCEPT",
			tapName, routed,
		),
		fmt.Sprintf(
			"-i %s -o %s -m state --state RELATED,ESTABLISHED -j ACCEPT",
			routed, tapName,
		),
		fmt.Sprintf(
			"-i %s -o %s -j ACCEPT",
			tapName, routed,
		),
	}
}

func getIPTablesNATRules(routed string) []string {
	return []string{
		fmt.Sprintf(
			"-o %s -j MASQUERADE",
			routed,
		),
	}
}
