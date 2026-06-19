package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/big"
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

	printAccountBalances := func(tenant *models.Tenant, accounts []*models.Account) {
		for _, account := range accounts {
			fmt.Printf("Account Name: %v, Balance: %.2f\n", account.Name, GetAccountBalance(ctx, client, tenant.TenantID, account.AccountID))
		}
	}

	fmt.Println("Creating Tenant")
	tenant, accounts := CreateTenant(ctx, client, "Joe Bob")
	fmt.Printf("Tenant Created: %v\n", tenant)
	printAccountBalances(tenant, accounts)

	fmt.Println("Transfering 25.0 from checking to savings")
	checking, savings := accounts[0], accounts[1]
	err = TransferFunds(ctx, client, tenant.TenantID, checking.AccountID, savings.AccountID, *new(big.Rat).SetFloat64(25.0))
	if err != nil {
		log.Fatalf("Failed to transfer funds, err: %v", err)
	}
	printAccountBalances(tenant, accounts)

	fmt.Println("Transfering 10000.0 from checking to savings")
	err = TransferFunds(ctx, client, tenant.TenantID, checking.AccountID, savings.AccountID, *new(big.Rat).SetFloat64(10000.0))
	if err != nil {
		fmt.Printf("Failed to transfer funds, err: %v\n", err)
	}
	printAccountBalances(tenant, accounts)

	printAccountTransactions := func(tenant *models.Tenant, account *models.Account) {
		transactions, err := GetAccountTransactions(ctx, client, tenant.TenantID, account.AccountID)
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
	printAccountTransactions(tenant, checking)
	printAccountTransactions(tenant, savings)
}

func CreateTenant(ctx context.Context, client *spanner.Client, name string) (*models.Tenant, []*models.Account) {
	tenant := &models.Tenant{
		TenantID: uuid.New().String(),
		Name:     name}

	accounts := []*models.Account{
		{
			TenantID:  tenant.TenantID,
			AccountID: uuid.New().String(),
			Name:      "Checking",
			Balance:   *new(big.Rat).SetFloat64(100.0),
		},
		{
			TenantID:  tenant.TenantID,
			AccountID: uuid.New().String(),
			Name:      "Savings",
			Balance:   *new(big.Rat).SetFloat64(50.0),
		},
	}

	mutList := []*spanner.Mutation{
		tenant.Insert(ctx),
		accounts[0].Insert(ctx),
		accounts[1].Insert(ctx),
	}

	_, err := client.Apply(ctx, mutList)
	if err != nil {
		log.Fatalf("CreateTenant failed, err: %v", err)
	}

	return tenant, accounts
}

func GetAccountBalance(ctx context.Context, client *spanner.Client, tenantId, accountId string) float64 {
	account, err := models.FindAccount(ctx, client.Single(), tenantId, accountId)
	if err != nil {
		log.Fatalf("GetAccount failed, err: %v", err)
	}
	balance, _ := account.Balance.Float64()
	return balance
}

func TransferFunds(ctx context.Context, client *spanner.Client, tenantID, fromAccountID, toAccountID string, amount big.Rat) error {
	_, err := client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		fromAccount, err := models.FindAccount(ctx, txn, tenantID, fromAccountID)
		if err != nil {
			return errors.Join(err, fmt.Errorf("Failed to find account id: %v, err: %v", fromAccountID, err))
		}
		toAccount, err := models.FindAccount(ctx, txn, tenantID, toAccountID)
		if err != nil {
			return errors.Join(err, fmt.Errorf("Failed to find account id: %v, err: %v", toAccountID, err))
		}

		if fromAccount.Balance.Cmp(&amount) < 0 {
			return fmt.Errorf("Account has insuffcient funds, id: %v", fromAccountID)
		}

		fromAccount.Balance.Sub(&fromAccount.Balance, &amount)
		fromAccountTrans := &models.Transaction{
			TransactionID:   uuid.New().String(),
			TenantID:        tenantID,
			AccountID:       fromAccount.AccountID,
			CreatedAt:       time.Now(),
			TransactionType: "DEBIT",
			Amount:          amount,
		}
		toAccount.Balance.Add(&toAccount.Balance, &amount)
		toAccountTrans := &models.Transaction{
			TransactionID:   uuid.New().String(),
			TenantID:        tenantID,
			AccountID:       toAccount.AccountID,
			CreatedAt:       time.Now(),
			TransactionType: "CREDIT",
			Amount:          amount,
		}
		err = txn.BufferWrite([]*spanner.Mutation{
			fromAccount.Update(ctx),
			toAccount.Update(ctx),
			fromAccountTrans.Insert(ctx),
			toAccountTrans.Insert(ctx)})
		if err != nil {
			return err
		}

		return nil
	})

	return err
}

func GetAccountTransactions(ctx context.Context, client *spanner.Client, tenantId, accountId string) ([]*models.Transaction, error) {
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
		return nil, fmt.Errorf("Failed to get transactions for account, tenantId: %v accountId: %v, err: %v", tenantId, accountId, err)
	}
	return transactions, nil
}
