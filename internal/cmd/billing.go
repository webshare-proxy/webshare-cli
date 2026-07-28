package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/webshare-proxy/webshare-cli/internal/output"
	webshare "github.com/webshare-proxy/webshare-go"
)

func newTransactionsCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "transactions",
		Short: "Inspect payment transactions",
	}
	var limit int
	list := &cobra.Command{
		Use:   "list",
		Short: "List payment transactions (newest first)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient(flags)
			if err != nil {
				return err
			}
			var transactions []webshare.Transaction
			for transaction, err := range client.Transactions.ListAll(cmd.Context(), webshare.TransactionListParams{}) {
				if err != nil {
					return err
				}
				transactions = append(transactions, transaction)
				if limit > 0 && len(transactions) >= limit {
					break
				}
			}
			if flags.asJSON {
				return output.JSON(os.Stdout, transactions)
			}
			rows := make([][]string, 0, len(transactions))
			for _, t := range transactions {
				rows = append(rows, []string{
					strconv.Itoa(t.ID),
					t.CreatedAt.Local().Format("2006-01-02"),
					fmt.Sprintf("$%.2f", t.Amount),
					string(t.Status),
					t.Reason,
				})
			}
			return output.Table(os.Stdout, []string{"ID", "DATE", "AMOUNT", "STATUS", "REASON"}, rows)
		},
	}
	list.Flags().IntVar(&limit, "limit", 25, "maximum number of transactions (0 for all)")
	cmd.AddCommand(list)
	return cmd
}

func newInvoicesCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "invoices",
		Short: "Download invoices",
	}
	var outPath string
	download := &cobra.Command{
		Use:   "download <transaction-id>",
		Short: "Download the invoice for a transaction as PDF",
		Long: `Download the invoice for a transaction as PDF. Find transaction IDs with
"webshare transactions list". The default output file is invoice-<id>.pdf;
pass -o - to write the PDF to stdout.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.Atoi(args[0])
			if err != nil {
				return usagef("transaction ID must be a number, got %q", args[0])
			}
			client, err := newClient(flags)
			if err != nil {
				return err
			}
			pdf, err := client.Invoices.Download(cmd.Context(), id)
			if err != nil {
				return err
			}
			if outPath == "-" {
				_, err := os.Stdout.Write(pdf)
				return err
			}
			path := outPath
			if path == "" {
				path = fmt.Sprintf("invoice-%d.pdf", id)
			}
			if err := os.WriteFile(path, pdf, 0o644); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "wrote %s (%d bytes)\n", path, len(pdf))
			return nil
		},
	}
	download.Flags().StringVarP(&outPath, "output", "o", "", "output file (default invoice-<id>.pdf, '-' for stdout)")
	cmd.AddCommand(download)
	return cmd
}
