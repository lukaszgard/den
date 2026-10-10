package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	"github.com/lukaszgard/den/internal/discover"
	"github.com/lukaszgard/den/internal/doctor"
	"github.com/lukaszgard/den/internal/platform"
	"github.com/lukaszgard/den/internal/service/awsauth"
)

// initCmd implements `den init`.
func initCmd() *cobra.Command {
	var profile, region, bastionID, envName, output string
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write a starter den.yaml from what an AWS account has",
		Long: "List the SSM-managed instances and the private data stores (RDS and Aurora,\n" +
			"ElastiCache, MemoryDB, DocumentDB, Redshift, OpenSearch, Neptune) that one AWS\n" +
			"profile can see in one region, and write a den.yaml that reaches them through\n" +
			"one of those instances. Read-only: it only calls Describe and List APIs.\n\n" +
			"The result is a normal static config to review and edit; den never discovers\n" +
			"at run time.",
		Example: "  den init --profile dev --region eu-central-1\n" +
			"  den init --profile dev -o ~/.config/den/den.yaml\n" +
			"  den init --profile dev --bastion i-0123456789abcdef0 --env dev -o den.yaml",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			stderr := cmd.ErrOrStderr()
			if profile == "" {
				profile = os.Getenv("AWS_PROFILE")
			}
			if profile == "" {
				return errors.New("--profile is required (or set AWS_PROFILE)")
			}
			awsCfg, err := awsauth.LoadConfig(ctx, profile, region)
			if err != nil {
				return fmt.Errorf("profile %s: %s", profile, doctor.ExplainAWSError(err))
			}
			if awsCfg.Region == "" {
				return fmt.Errorf("no region: pass --region, or set one for profile %s", profile)
			}
			region = awsCfg.Region

			fmt.Fprintf(stderr, "Looking in %s as %s…\n", region, profile)
			inv, warnings := discover.Run(ctx, discover.NewClients(awsCfg))
			for _, w := range warnings {
				se, ok := errors.AsType[*discover.SourceError](w)
				if ok && se.Source == "SSM instances" {
					return fmt.Errorf("could not list SSM instances: %s", doctor.ExplainAWSError(se.Err))
				}
				if ok {
					fmt.Fprintf(stderr, "  skipped %s: %s\n", se.Source, doctor.ExplainAWSError(se.Err))
				}
			}
			bastion, err := pickBastion(inv, bastionID, isTerminal(os.Stdin) && isTerminal(os.Stderr))
			if err != nil {
				return err
			}
			if envName == "" {
				envName = region
			}
			yaml := discover.Generate(inv, discover.GenerateOptions{
				Profile: profile, Region: region, EnvName: envName, Bastion: bastion, Generated: time.Now(),
			})
			where, err := writeGenerated(cmd.OutOrStdout(), output, yaml, force)
			if err != nil {
				return err
			}
			fmt.Fprintf(stderr, "Found %s behind %s.\n", inv.Summary(), bastion.InstanceID)
			if where != "" {
				fmt.Fprintf(stderr, "Wrote %s. Review it, then check your setup with: den doctor -c %s\n", where, where)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&profile, "profile", "", "AWS profile to look with, and to put in the config (default $AWS_PROFILE)")
	f.StringVar(&region, "region", "", "AWS region (default: the profile's)")
	f.StringVar(&bastionID, "bastion", "", "instance ID of the SSM-managed instance to tunnel through")
	f.StringVar(&envName, "env", "", "name of the generated environment (default: the region)")
	f.StringVarP(&output, "output", "o", "-", "file to write, or - for stdout")
	f.BoolVar(&force, "force", false, "overwrite the output file if it exists")
	return cmd
}

// pickBastion chooses the instance the services tunnel through: the one named,
// the only one online, or — at a terminal — the one the user picks.
func pickBastion(inv discover.Inventory, id string, interactive bool) (discover.Bastion, error) {
	if id != "" {
		for _, b := range inv.Bastions {
			if b.InstanceID == id {
				return b, nil
			}
		}
		return discover.Bastion{}, fmt.Errorf("%s is not an SSM-managed EC2 instance in this region", id)
	}
	online := inv.Online()
	switch {
	case len(online) == 1:
		return online[0], nil
	case len(online) == 0:
		return discover.Bastion{}, errors.New("no SSM-managed EC2 instance is online here; den needs one to tunnel through (pass --bastion to name one anyway)")
	case !interactive:
		ids := make([]string, len(online))
		for i, b := range online {
			ids[i] = b.InstanceID + " (" + b.Name + ")"
		}
		return discover.Bastion{}, fmt.Errorf("several instances could be the bastion — pass --bastion with one of: %s", strings.Join(ids, ", "))
	}
	opts := make([]huh.Option[string], len(online))
	for i, b := range online {
		opts[i] = huh.NewOption(b.InstanceID+"  "+b.Name, b.InstanceID)
	}
	var chosen string
	sel := huh.NewSelect[string]().Title("Tunnel through which instance?").
		Description("It needs network access to the data stores.").Options(opts...).Value(&chosen)
	if err := huh.NewForm(huh.NewGroup(sel)).WithTheme(standaloneTheme()).Run(); err != nil {
		return discover.Bastion{}, errors.New("cancelled")
	}
	return pickBastion(inv, chosen, false)
}

// writeGenerated writes the config to stdout ("-") or to a file it will not
// overwrite unless forced, and returns the file written ("" for stdout).
func writeGenerated(stdout io.Writer, output, content string, force bool) (string, error) {
	if output == "-" {
		_, err := io.WriteString(stdout, content)
		return "", err
	}
	path := platform.ExpandHome(output)
	if _, err := os.Stat(path); err == nil && !force {
		return "", fmt.Errorf("%s exists; pass --force to overwrite it, or -o - to print instead", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	return path, nil
}
