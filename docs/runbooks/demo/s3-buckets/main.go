// Lists the newest S3 buckets the environment's profile can see: a runbook in Go
// with the AWS SDK for Go v2. den sets AWS_PROFILE and AWS_REGION, which the
// default credential chain reads, so the client needs no configuration.
// Read-only: s3:ListAllMyBuckets.
package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// shown is how many buckets are printed: the output stays one screen.
const shown = 10

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "s3-buckets:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return err
	}

	var buckets []types.Bucket
	pages := s3.NewListBucketsPaginator(s3.NewFromConfig(cfg), &s3.ListBucketsInput{})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return err
		}
		buckets = append(buckets, page.Buckets...)
	}
	sort.Slice(buckets, func(i, j int) bool {
		return buckets[i].CreationDate.After(*buckets[j].CreationDate)
	})

	fmt.Printf("%d buckets, newest first:\n", len(buckets))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, b := range buckets[:min(len(buckets), shown)] {
		age := int(time.Since(*b.CreationDate).Hours() / 24)
		fmt.Fprintf(w, "  %s\t%s\t%dd old\n", *b.Name, b.CreationDate.Format(time.DateOnly), age)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if len(buckets) > shown {
		fmt.Printf("  … and %d more\n", len(buckets)-shown)
	}
	return nil
}
