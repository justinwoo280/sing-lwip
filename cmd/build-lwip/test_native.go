package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func testNativeCommand(root string, args []string) error {
	flags := flag.NewFlagSet("test-native", flag.ContinueOnError)
	compiler := flags.String("cc", envDefault("CC", "cc"), "C compiler command")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	cc, err := commandWords(*compiler)
	if err != nil {
		return err
	}
	sources, err := wrappers(root)
	if err != nil {
		return err
	}
	directory, err := os.MkdirTemp("", "sing-lwip-native-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	// abi.c is a shared-library FFI fixture, not an executable C regression.
	for _, name := range []string{"allocator", "gso", "rx_buffer", "udp_netif"} {
		binary := filepath.Join(directory, name)
		command := []string{"-O1", "-g", "-fsanitize=address,undefined", "-fno-omit-frame-pointer"}
		if name == "udp_netif" {
			command = append(command, "-DSING_LWIP_STANDALONE")
		}
		command = append(command, includes(root)...)
		command = append(command, sources...)
		command = append(command, filepath.Join(root, "tests", name+".c"), "-o", binary)
		if err := run(root, nil, cc, command...); err != nil {
			return err
		}
		if err := run(root, map[string]string{"ASAN_OPTIONS": "detect_leaks=1", "UBSAN_OPTIONS": "halt_on_error=1"}, []string{binary}); err != nil {
			return err
		}
		fmt.Println(name, "PASS")
	}
	return nil
}

func testUDPCommand(root string, args []string) error {
	flags := flag.NewFlagSet("test-upstream-udp", flag.ContinueOnError)
	compiler := flags.String("cc", envDefault("CC", "cc"), "C compiler command")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	cc, err := commandWords(*compiler)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(root, "third_party/lwip/src/core/udp.c"))
	if err != nil {
		return err
	}
	const fixed = "if ((pcb->pretend_netif_idx == netif_get_index(inp)) &&\n          (pcb->remote_port == src) && ip_addr_cmp(&pcb->remote_ip, ip_current_src_addr()))"
	const unfixed = "if ((pcb->remote_port == src) && ip_addr_cmp(&pcb->remote_ip, ip_current_src_addr()))"
	text := string(data)
	if strings.Count(text, fixed) != 1 {
		return fmt.Errorf("expected exactly one UDP ingress-netif predicate")
	}
	sources, err := wrappers(root)
	if err != nil {
		return err
	}
	directory, err := os.MkdirTemp("", "sing-lwip-upstream-udp-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	for _, phase := range []struct {
		name, source     string
		exit, mismatches int
	}{{"before", strings.Replace(text, fixed, unfixed, 1), 1, 4}, {"after", text, 0, 0}} {
		udp, binary := filepath.Join(directory, phase.name+".c"), filepath.Join(directory, phase.name)
		if err := os.WriteFile(udp, []byte(phase.source), 0o644); err != nil {
			return err
		}
		command := append([]string{"-O2", "-DSING_LWIP_STANDALONE"}, includes(root)...)
		for _, source := range sources {
			if filepath.Base(source) != "lwip_core_udp.c" {
				command = append(command, source)
			}
		}
		command = append(command, udp, filepath.Join(root, "tests/udp_netif.c"), "-o", binary)
		if err := run(root, nil, cc, command...); err != nil {
			return err
		}
		result, err := process(root, nil, []string{binary}).CombinedOutput()
		exit := 0
		if err != nil {
			var failure *exec.ExitError
			if !errors.As(err, &failure) {
				return err
			}
			exit = failure.ExitCode()
		}
		fmt.Printf("%s: exit=%d\n%s", phase.name, exit, result)
		if exit != phase.exit {
			return fmt.Errorf("%s: expected exit %d, got %d", phase.name, phase.exit, exit)
		}
		for _, version := range []int{4, 6} {
			if !strings.Contains(string(result), fmt.Sprintf("IPv%d: mismatches=%d", version, phase.mismatches)) {
				return fmt.Errorf("%s: unexpected IPv%d result", phase.name, version)
			}
		}
	}
	return nil
}
