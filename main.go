package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/big"
	"strings"
	"time"

	"cloud-spanner-ledger/models"

	"cloud.google.com/go/spanner"
	"github.com/google/uuid"
)

func main() {
	ctx := context.Background()
	dbUri := "projects/test-project/instances/test-instance/databases/ledger-db"
	client, err := spanner.NewClient(ctx, dbUri)
	if err != nil {
		log.Fatalf("Failed to create Spanner client: %v", err)
	}
	defer client.Close()

	printAccountBalances := func(tenant *TenantWithAccounts) {
		for _, account := range tenant.Accounts {
			fmt.Printf("Account Name: %v, Balance: %.2f\n", account.Name, GetAccountBalance(ctx, client, tenant.Tenant.TenantID, account.AccountID))
		}
	}

	fmt.Println("Creating Tenant")
	tenant := CreateTenant(ctx, client, "Joe Bob")
	fmt.Printf("Tenant Created: %v\n", tenant)
	printAccountBalances(tenant)

	fmt.Println("Transfering 25.0 from checking to savings")
	_, err = TransferFunds(ctx, client, tenant.Tenant.TenantID,
		tenant.Accounts[Checking].AccountID, tenant.Accounts[Savings].AccountID,
		*new(big.Rat).SetFloat64(25.0), uuid.New().String())
	if err != nil {
		log.Fatalf("Failed to transfer funds, err: %v", err)
	}
	printAccountBalances(tenant)

	fmt.Println("Transfering 10000.0 from checking to savings")
	_, err = TransferFunds(ctx, client, tenant.Tenant.TenantID,
		tenant.Accounts[Checking].AccountID,
		tenant.Accounts[Savings].AccountID,
		*new(big.Rat).SetFloat64(10000.0), uuid.New().String())
	if err != nil {
		fmt.Printf("Failed to transfer funds, err: %v\n", err)
	}
	printAccountBalances(tenant)

	printAccountTransactions := func(tenant *TenantWithAccounts,
		accountType AccountType) {
		account := tenant.Accounts[accountType]
		transactions, err := GetAccountTransactions(ctx, client,
			tenant.Tenant.TenantID, account.AccountID)
		if err != nil {
			log.Fatal(err)
		}

		fmt.Printf("Transactions for account name: %v\n", account.Name)
		for _, transaction := range transactions {
			amount, _ := transaction.Amount.Float64()
			fmt.Printf("transaction: type: %v createdAt: %v amount: %v\n",
				transaction.TransactionType,
				transaction.CreatedAt,
				amount)
		}
	}
	printAccountTransactions(tenant, Checking)
	printAccountTransactions(tenant, Savings)

	auditReport, err := GetTenantAuditReport(ctx, client, tenant.Tenant.TenantID,
		spanner.StrongRead())
	if err != nil {
		log.Fatalf("Failed to GetTenantAuditReport, err: %v", err)
	}
	fmt.Printf("AuditReport:\n%v", auditReport.String())

	fmt.Println("Creating new tenant: Susan")
	newTenant := CreateTenant(ctx, client, "Susan")
	fmt.Println("Transfering 10.0 from checking to savings")
	transactions, err := TransferFunds(ctx, client, newTenant.Tenant.TenantID,
		newTenant.Accounts[Checking].AccountID,
		newTenant.Accounts[Savings].AccountID,
		*new(big.Rat).SetFloat64(10.0), uuid.New().String())
	if err != nil {
		log.Fatalf("Failed to TransferFunds, err: %v", err)
	}

	time.Sleep(5 * time.Second)
	auditReport, err = GetTenantAuditReport(ctx, client, newTenant.Tenant.TenantID,
		spanner.ExactStaleness(10*time.Second))
	if err != nil {
		log.Fatalf("Failed to GetTenantAudiReport, err: %v", err)
	}
	fmt.Printf("AuditReport:\n%v", auditReport.String())
	referenceId := transactions[0].ReferenceID

	fmt.Printf("Getting Transactions for Ref: %v\n", referenceId)
	transactions, err = GetTransactionsByRef(ctx, client, referenceId)
	if err != nil {
		log.Fatalf("Failed: GetTransactionsByRef, err: %v", err)
	}
	for _, transaction := range transactions {
		fmt.Printf("Transaction: %v\n", transaction)
	}
}

type TenantWithAccounts struct {
	Tenant   *models.Tenant
	Accounts map[AccountType]*models.Account
}

type AccountType string

func (t AccountType) String() string {
	return string(t)
}

const (
	Checking AccountType = "Checkings"
	Savings  AccountType = "Savings"
)

const DefaultCheckingBalance float64 = 100.0
const DefaultSavingsBalance float64 = 50.0

func CreateTenant(ctx context.Context, client *spanner.Client, name string) *TenantWithAccounts {
	tenant := &models.Tenant{
		TenantID: uuid.New().String(),
		Name:     name}

	accounts := map[AccountType]*models.Account{
		Checking: {
			TenantID:  tenant.TenantID,
			AccountID: uuid.New().String(),
			Name:      Checking.String(),
			Balance:   *new(big.Rat).SetFloat64(DefaultCheckingBalance),
		},
		Savings: {
			TenantID:  tenant.TenantID,
			AccountID: uuid.New().String(),
			Name:      Savings.String(),
			Balance:   *new(big.Rat).SetFloat64(DefaultSavingsBalance),
		},
	}

	mutList := make([]*spanner.Mutation, 0, 3)
	mutList = append(mutList, tenant.Insert(ctx))
	for _, account := range accounts {
		mutList = append(mutList, account.Insert(ctx))
	}

	_, err := client.Apply(ctx, mutList)
	if err != nil {
		log.Fatalf("CreateTenant failed, err: %v", err)
	}

	return &TenantWithAccounts{
		Tenant:   tenant,
		Accounts: accounts,
	}
}

func GetAccountBalance(ctx context.Context, client *spanner.Client, tenantId, accountId string) float64 {
	account, err := models.FindAccount(ctx, client.Single(), tenantId, accountId)
	if err != nil {
		log.Fatalf("GetAccount failed, err: %v", err)
	}
	balance, _ := account.Balance.Float64()
	return balance
}

type TransactionType string

const (
	Debit  TransactionType = "DEBIT"
	Credit TransactionType = "CREDIT"
)

func (t TransactionType) String() string {
	return string(t)
}

func TransferFunds(ctx context.Context, client *spanner.Client, tenantID,
	fromAccountID, toAccountID string, amount big.Rat,
	referenceId string) ([]*models.Transaction, error) {
	createdAt := time.Now()
	fromAccountTrans := &models.Transaction{
		TransactionID:   uuid.New().String(),
		TenantID:        tenantID,
		AccountID:       fromAccountID,
		CreatedAt:       createdAt,
		TransactionType: Debit.String(),
		Amount:          amount,
		ReferenceID:     referenceId,
	}
	toAccountTrans := &models.Transaction{
		TransactionID:   uuid.New().String(),
		TenantID:        tenantID,
		AccountID:       toAccountID,
		CreatedAt:       createdAt,
		TransactionType: Credit.String(),
		Amount:          amount,
		ReferenceID:     referenceId,
	}
	_, err := client.ReadWriteTransaction(ctx, func(ctx context.Context,
		txn *spanner.ReadWriteTransaction) error {
		fromAccount, err := models.FindAccount(ctx, txn, tenantID, fromAccountID)
		if err != nil {
			return errors.Join(err,
				fmt.Errorf("failed to find account id: %v, err: %v",
					fromAccountID,
					err))
		}
		toAccount, err := models.FindAccount(ctx, txn, tenantID, toAccountID)
		if err != nil {
			return errors.Join(err,
				fmt.Errorf("failed to find account id: %v, err: %v",
					toAccountID,
					err))
		}

		if fromAccount.Balance.Cmp(&amount) < 0 {
			return fmt.Errorf("account has insuffcient funds, id: %v", fromAccountID)
		}

		fromAccount.Balance.Sub(&fromAccount.Balance, &amount)
		toAccount.Balance.Add(&toAccount.Balance, &amount)
		err = txn.BufferWrite([]*spanner.Mutation{
			fromAccount.Update(ctx),
			toAccount.Update(ctx),
			fromAccountTrans.Insert(ctx),
			toAccountTrans.Insert(ctx)})
		if err != nil {
			return err
		}

		return err
	})

	if err != nil {
		return nil, err
	}

	return []*models.Transaction{fromAccountTrans, toAccountTrans}, nil
}

func GetAccountTransactions(ctx context.Context, client *spanner.Client,
	tenantId, accountId string) ([]*models.Transaction, error) {
	transactions, err := models.ReadTransaction(
		ctx,
		client.Single(),
		spanner.KeyRange{
			Start: spanner.Key{tenantId, accountId},
			End:   spanner.Key{tenantId, accountId},
			Kind:  spanner.ClosedClosed,
		},
	)
	if err != nil {
		return nil,
			fmt.Errorf("failed to get transactions for account, tenantId: %v accountId: %v, err: %v",
				tenantId,
				accountId,
				err)
	}
	return transactions, nil
}

type AuditReport struct {
	TotalCurrentBalances float64
	TransactionVolumes   map[TransactionType]float64
}

func NewAuditReport() *AuditReport {
	report := &AuditReport{
		TransactionVolumes:   make(map[TransactionType]float64),
		TotalCurrentBalances: 0.0,
	}
	report.TransactionVolumes[Debit] = 0.0
	report.TransactionVolumes[Credit] = 0.0
	return report
}

func (report *AuditReport) AddBalance(balance float64) {
	report.TotalCurrentBalances += balance
}

func (report *AuditReport) AddTransaction(t TransactionType, amount float64) {
	report.TransactionVolumes[t] += amount
}

func (report *AuditReport) String() string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("TotalCurrentBalances: %v\n", //nolint:staticcheck
		report.TotalCurrentBalances))
	for txnType, txnVolume := range report.TransactionVolumes {
		builder.WriteString( //nolint:staticcheck
			fmt.Sprintf("TotalTransactionVolumes: type: %v amount: %v\n",
				txnType,
				txnVolume))
	}
	return builder.String()
}

func GetTenantAuditReport(ctx context.Context, client *spanner.Client,
	tenantId string, timestampBound spanner.TimestampBound) (*AuditReport, error) {
	txn := client.ReadOnlyTransaction().WithTimestampBound(timestampBound)
	defer txn.Close()

	report := NewAuditReport()
	accounts, err := models.ReadAccount(ctx, txn,
		spanner.KeyRange{
			Start: spanner.Key{tenantId},
			End:   spanner.Key{tenantId},
			Kind:  spanner.ClosedClosed,
		},
	)
	if err != nil {
		return nil, err
	}

	for _, account := range accounts {
		balance, _ := account.Balance.Float64()
		report.AddBalance(balance)
		transactions, err := models.ReadTransaction(ctx, txn,
			spanner.KeyRange{
				Start: spanner.Key{tenantId, account.AccountID},
				End:   spanner.Key{tenantId, account.AccountID},
				Kind:  spanner.ClosedClosed,
			},
		)
		if err != nil {
			return nil, err
		}
		for _, transaction := range transactions {
			amount, _ := transaction.Amount.Float64()
			report.AddTransaction(TransactionType(transaction.TransactionType), amount)
		}
	}

	return report, nil
}

func GetTransactionsByRef(ctx context.Context, client *spanner.Client,
	referenceId string) ([]*models.Transaction, error) {
	transactions, err := models.FindTransactionsByAmountTransactionTypeReferenceID(
		ctx,
		client.Single(),
		referenceId,
	)
	if err != nil {
		return nil, err
	}
	return transactions, nil
}
