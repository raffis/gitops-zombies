package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"

	gitopszombiesv1 "github.com/raffis/gitops-zombies/pkg/apis/gitopszombies/v1"
	"github.com/raffis/gitops-zombies/pkg/detector"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/homedir"
	"k8s.io/klog/v2"
	k8sget "k8s.io/kubectl/pkg/cmd/get"
)

const (
	version = "0.0.0-dev"
	commit  = "none"
	date    = "unknown"
)

type args struct {
	gitopszombiesv1.Config
	printConfig bool
	version     bool
}

const (
	statusOK = iota
	statusFail
	statusZombiesDetected

	statusAnnotation = "status"

	flagExcludeCluster = "exclude-cluster"
	flagFail           = "fail"
	flagIncludeAll     = "include-all"
	flagLabelSelector  = "selector"
	flagNoStream       = "no-stream"
)

func main() {
	rootCmd, err := parseCliArgs()
	if err != nil {
		fmt.Printf("%v", err)
	}

	err = rootCmd.Execute()
	if err != nil {
		fmt.Printf("%v", err)
	}

	os.Exit(toExitCode(rootCmd.Annotations[statusAnnotation]))
}

func toExitCode(codeStr string) int {
	code, err := strconv.Atoi(codeStr)
	if err != nil {
		return statusFail
	}

	return code
}

func boolPtr(b bool) *bool {
	newBool := b
	return &newBool
}

func strPtr(str string) *string {
	s := str
	return &s
}

func strNilOrDefault(str *string, dflt string) string {
	if str != nil {
		return *str
	}
	return dflt
}

func boolNilOrDefault(b *bool, dflt bool) bool {
	if b != nil {
		return *b
	}
	return dflt
}

func parseCliArgs() (*cobra.Command, error) {
	flags := args{Config: gitopszombiesv1.Config{
		TypeMeta:         metav1.TypeMeta{},
		ExcludeClusters:  nil,
		ExcludeResources: nil,
		Fail:             boolPtr(false),
		IncludeAll:       boolPtr(false),
		LabelSelector:    strPtr(""),
		NoStream:         boolPtr(false),
	}}
	kubeconfigArgs := genericclioptions.NewConfigFlags(false)
	printFlags := k8sget.NewGetPrintFlags()
	cfgFile := path.Join(homedir.HomeDir(), ".gitops-zombies.yaml")

	rootCmd := &cobra.Command{
		Use:           "gitops-zombies",
		SilenceUsage:  true,
		SilenceErrors: true,
		Short:         "Find kubernetes resources which are not managed by GitOps",
		Long:          `Finds all kubernetes resources from all installed apis on a kubernetes cluste and evaluates whether they are managed by a flux kustomization or a helmrelease.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.Annotations = make(map[string]string)
			cmd.Annotations[statusAnnotation] = strconv.Itoa(statusFail)

			if flags.version {
				fmt.Printf(`{"version":"%s","sha":"%s","date":"%s"}`+"\n", version, commit, date)
				cmd.Annotations[statusAnnotation] = strconv.Itoa(statusOK)
				return nil
			}

			conf, err := loadConfig(cfgFile)
			if err != nil {
				return err
			}

			mergeConfigAndFlags(conf, flags.Config, cmd)

			if flags.printConfig {
				printConfig(conf)
				return nil
			}

			status, err := run(conf, kubeconfigArgs, printFlags)
			if err != nil {
				return err
			}

			cmd.Annotations[statusAnnotation] = strconv.Itoa(status)
			return nil
		},
	}

	apiServer := ""
	kubeconfigArgs.APIServer = &apiServer
	kubeconfigArgs.AddFlags(rootCmd.PersistentFlags())

	rest.SetDefaultWarningHandler(rest.NewWarningWriter(io.Discard, rest.WarningWriterOptions{}))
	set := &flag.FlagSet{}
	klog.InitFlags(set)
	rootCmd.PersistentFlags().AddGoFlagSet(set)

	err := rootCmd.RegisterFlagCompletionFunc("context", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return contextsCompletionFunc(kubeconfigArgs, toComplete)
	})
	if err != nil {
		return nil, err
	}

	rootCmd.Flags().StringVarP(&cfgFile, "config", "", cfgFile, "Config file")
	rootCmd.Flags().StringVarP(printFlags.OutputFormat, "output", "o", *printFlags.OutputFormat, fmt.Sprintf(`Output format. One of: (%s). See custom columns [https://kubernetes.io/docs/reference/kubectl/overview/#custom-columns], golang template [http://golang.org/pkg/text/template/#pkg-overview] and jsonpath template [https://kubernetes.io/docs/reference/kubectl/jsonpath/].`, strings.Join(printFlags.AllowedFormats(), ", ")))
	rootCmd.Flags().BoolVarP(&flags.version, "version", "", flags.version, "Print version and exit")
	rootCmd.Flags().BoolVarP(&flags.printConfig, "print-config", "p", flags.printConfig, "Print config which will be loaded and exit")
	rootCmd.Flags().BoolVarP(flags.IncludeAll, flagIncludeAll, "a", false, "Includes resources which are considered dynamic resources")
	rootCmd.Flags().StringVarP(flags.LabelSelector, flagLabelSelector, "l", "", "Label selector (Is used for all apis)")
	rootCmd.Flags().BoolVarP(flags.NoStream, flagNoStream, "", false, "Display discovered resources at the end instead of live")
	rootCmd.Flags().BoolVarP(flags.Fail, flagFail, "", false, "Exit with an exit code > 0 if zombies are detected")
	flags.ExcludeClusters = rootCmd.Flags().StringSliceP(flagExcludeCluster, "", nil, "Exclude cluster from zombie detection (default none)")

	rootCmd.DisableAutoGenTag = true
	rootCmd.SetOut(os.Stdout)
	return rootCmd, nil
}

func loadConfig(configPath string) (*gitopszombiesv1.Config, error) {
	if _, err := os.Stat(configPath); err != nil {
		klog.V(1).Infof("Can't find config file at %s", configPath)
		return &gitopszombiesv1.Config{}, nil
	}

	json, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	err = gitopszombiesv1.AddToScheme(scheme.Scheme)
	if err != nil {
		return nil, err
	}

	obj, err := runtime.Decode(scheme.Codecs.UniversalDeserializer(), json)
	if err != nil {
		return nil, err
	}

	var cfg gitopszombiesv1.Config
	switch o := obj.(type) {
	case *gitopszombiesv1.Config:
		cfg = *o
	default:
		err = errors.New("unsupported config")
		return nil, err
	}

	return &cfg, nil
}

func mergeConfigAndFlags(conf *gitopszombiesv1.Config, flags gitopszombiesv1.Config, cmd *cobra.Command) {
	// cmd line overrides config
	if cmd.Flags().Changed(flagExcludeCluster) {
		conf.ExcludeClusters = flags.ExcludeClusters
	}

	if cmd.Flags().Changed(flagFail) {
		conf.Fail = flags.Fail
	}

	if cmd.Flags().Changed(flagIncludeAll) {
		conf.IncludeAll = flags.IncludeAll
	}

	if cmd.Flags().Changed(flagLabelSelector) {
		conf.LabelSelector = flags.LabelSelector
	}

	if cmd.Flags().Changed(flagNoStream) {
		conf.NoStream = flags.NoStream
	}
}

func printConfig(conf *gitopszombiesv1.Config) {
	fmt.Printf("fail: %t\n", boolNilOrDefault(conf.Fail, false))
	fmt.Printf("includeAll: %t\n", boolNilOrDefault(conf.IncludeAll, false))
	fmt.Printf("selector: %s\n", strNilOrDefault(conf.LabelSelector, ""))
	fmt.Printf("noStream: %t\n", boolNilOrDefault(conf.NoStream, false))

	fmt.Println("excludeClusters:")
	for _, c := range *conf.ExcludeClusters {
		fmt.Printf(" - %s\n", c)
	}
	fmt.Println("excludeResources:")
	for _, r := range conf.ExcludeResources {
		fmt.Printf(" - name: %s\n   namespace: %s\n   apiVersion\n   kind: %s\n   cluster: %s\n", strNilOrDefault(r.Name, ".*"), strNilOrDefault(r.Namespace, ".*"), r.APIVersion, r.Kind)
	}
}

func run(conf *gitopszombiesv1.Config, kubeconfigArgs *genericclioptions.ConfigFlags, printFlags *k8sget.PrintFlags) (int, error) {
	// default processing
	detect, err := detector.New(conf, kubeconfigArgs, printFlags)
	if err != nil {
		return statusFail, err
	}
	resourceCount, allZombies, err := detect.DetectZombies()
	if err != nil {
		return statusFail, err
	}

	if conf.NoStream != nil && *conf.NoStream {
		err = detect.PrintZombies(allZombies)
		if err != nil {
			return statusFail, err
		}
	}

	var totalZombies int
	for _, zombies := range allZombies {
		totalZombies += len(zombies)
	}

	if conf.NoStream != nil && *conf.NoStream && printFlags.OutputFormat != nil && *printFlags.OutputFormat == "" {
		fmt.Printf("\nSummary: %d resources found, %d zombies detected\n", resourceCount, totalZombies)
	}

	if conf.Fail != nil && *conf.Fail && totalZombies > 0 {
		return statusZombiesDetected, nil
	}

	return statusOK, nil
}
