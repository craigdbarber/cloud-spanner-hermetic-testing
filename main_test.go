package main

import (
	"bytes"
	"cloud-spanner-ledger/models"
	"context"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	adminApi "cloud.google.com/go/spanner/admin/database/apiv1"
	"cloud.google.com/go/spanner/admin/database/apiv1/databasepb"
	instanceAdminApi "cloud.google.com/go/spanner/admin/instance/apiv1"
	"cloud.google.com/go/spanner/admin/instance/apiv1/instancepb"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

const TestProject string = "test-project"
const TestInstanceId string = "test-instance"
const TestInstance string = "projects/test-project/instances/test-instance"
const EmulatorContainerName string = "test-spanner-harness"

func TestMain(m *testing.M) {
	var cleanupFunc func()
	if os.Getenv("SPANNER_EMULATOR_HOST") == "" {
		cmd := exec.Command("docker",
			"run",
			"-d",
			"--name",
			EmulatorContainerName,
			"-p",
			"9010:9010",
			"-p",
			"9020:9020",
			"gcr.io/cloud-spanner-emulator/emulator",
		)
		var stdoutBuf, stderrBuf bytes.Buffer
		cmd.Stdout = &stdoutBuf
		cmd.Stderr = &stderrBuf
		err := cmd.Run()
		if err != nil {
			fmt.Printf("Command failed with stdout: %v\nstderr: %v\n", stdoutBuf.String(), stderrBuf.String())
			log.Fatalf("Failed to start spanner emulator, err: %v", err)
		}

		timeout := time.Now().Add(10 * time.Second)
		emulatorStarted := false
		for !emulatorStarted && time.Now().Before(timeout) {
			resp, err := http.Get(fmt.Sprintf("http://localhost:9020/v1/projects/%v/instances", TestProject))
			if err != nil {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			err = resp.Body.Close()
			if err != nil {
				log.Fatalf("Faled to close response body, err: %v", err)
			}
			emulatorStarted = true
		}
		if !emulatorStarted {
			log.Fatalf("Start spanner emulator timed out")
		}

		err = os.Setenv("SPANNER_EMULATOR_HOST", "localhost:9010")
		if err != nil {
			log.Fatalf("Failed to set env var: 'SPANNER_EMULATOR_HOST', err: %v", err)
		}

		cleanupFunc = func() {
			cmd = exec.Command("docker", "stop", EmulatorContainerName)
			err = cmd.Run()
			if err != nil {
				log.Fatalf("Failed to stop spanner emulator container")
			}
			cmd = exec.Command("docker", "rm", EmulatorContainerName)
			err = cmd.Run()
			if err != nil {
				log.Fatalf("Failed to rm spanner emulator container")
			}
		}
	}

	code := m.Run()
	if cleanupFunc != nil {
		cleanupFunc()
	}
	os.Exit(code)
}

func setupTestDB(t *testing.T) (*spanner.Client, func()) {
	instanceAdminClient, err := instanceAdminApi.NewInstanceAdminClient(
		context.Background())
	if err != nil {
		log.Fatalf("Failed to create instance admin client, err: %v", err)
	}
	defer instanceAdminClient.Close() //nolint:errcheck

	_, err = instanceAdminClient.GetInstance(t.Context(), &instancepb.GetInstanceRequest{
		Name: TestInstance,
	})
	if err != nil {
		op, err := instanceAdminClient.CreateInstance(t.Context(),
			&instancepb.CreateInstanceRequest{
				Parent:     fmt.Sprintf("projects/%s", TestProject),
				InstanceId: TestInstanceId,
				Instance: &instancepb.Instance{
					Config:      fmt.Sprintf("projects/%s/instanceConfigs/emulator-config", TestProject),
					DisplayName: "Local Testing Instance",
					NodeCount:   1,
					Labels:      map[string]string{"env": "development"},
				},
			})
		if err != nil {
			log.Fatalf("Failed to create test instance")
		}
		_, err = op.Wait(t.Context())
		if err != nil {
			log.Fatalf("Failed waiting on create instance op, err: %v", err)
		}
	}

	adminClient, err := adminApi.NewDatabaseAdminClient(t.Context())
	if err != nil {
		t.Fatalf("Failed to get DB Admin Client, err: %v", err)
	}

	file, err := os.ReadFile("schema.sql")
	if err != nil {
		t.Fatalf("Failed to read DB schema, err: %v", err)
	}
	var tableCreateStatements []string
	for _, statement := range strings.Split(string(file), ";") {
		statement = strings.TrimSpace(statement)
		if statement != "" {
			tableCreateStatements = append(tableCreateStatements, statement)
		}
	}

	testDbName := fmt.Sprintf("test-db-%v", uuid.New())[:30]
	op, err := adminClient.CreateDatabase(t.Context(), &databasepb.CreateDatabaseRequest{
		Parent:          TestInstance,
		CreateStatement: fmt.Sprintf("CREATE DATABASE `%v`", testDbName),
		ExtraStatements: tableCreateStatements,
	})
	if err != nil {
		t.Fatalf("Failed to create test database, err: %v", err)
	}
	_, err = op.Wait(t.Context())
	if err != nil {
		t.Fatalf("Failed waiting on create database op, err: %v", err)
	}

	dbUri := fmt.Sprintf("%v/databases/%v", TestInstance, testDbName)
	client, err := spanner.NewClient(t.Context(), dbUri)
	if err != nil {
		t.Fatalf("Failed to get spanner client, err: %v", err)
	}

	cleanupFunc := func() {
		err = adminClient.DropDatabase(t.Context(), &databasepb.DropDatabaseRequest{
			Database: dbUri,
		})
		if err != nil {
			t.Fatalf("Failed to drop database, err: %v", err)
		}
		adminClient.Close() //nolint:errcheck
		client.Close()      //nolint:errcheck
	}

	return client, cleanupFunc
}

func TestCreateTenant(t *testing.T) {
	client, cleanupFunc := setupTestDB(t)
	defer cleanupFunc()

	tenantWithAccount := CreateTenant(t.Context(), client, "Jim Belushi")
	tenant, err := models.FindTenant(t.Context(), client.Single(), tenantWithAccount.Tenant.TenantID)
	if err != nil {
		t.Fatalf("Failed to find tenant, err: %v", err)
	}
	assert.NotNil(t, tenant, "tenant should not be nil")
	assert.Equal(t, tenant.Name, tenantWithAccount.Tenant.Name, "tenant name should match")

	accounts, err := models.ReadAccount(t.Context(), client.Single(), spanner.KeyRange{
		Start: spanner.Key{tenantWithAccount.Tenant.TenantID},
		End:   spanner.Key{tenantWithAccount.Tenant.TenantID},
		Kind:  spanner.ClosedClosed,
	})
	if err != nil {
		t.Fatalf("Failed to find acounts, err: %v", err)
	}
	assert.Equal(t, len(accounts), 2, "should have found two accounts")
	for _, account := range accounts {
		if account.Name == Checking.String() {
			gotBalance, _ := account.Balance.Float64()
			assert.Equal(t, gotBalance, DefaultCheckingBalance, "checking balance should be default")
		} else if account.Name == Savings.String() {
			gotBalance, _ := account.Balance.Float64()
			assert.Equal(t, gotBalance, DefaultSavingsBalance, "checking balance should be default")
		} else {
			t.Fatalf("Got back unexpected account name: %v", account.Name)
		}
	}
}

func TestTransferFunds(t *testing.T) {
	client, cleanupFunc := setupTestDB(t)
	defer cleanupFunc()

	tenantWithAccount := CreateTenant(t.Context(), client, "Homer Simpson")
	amount := 10.0
	_, err := TransferFunds(t.Context(), client,
		tenantWithAccount.Tenant.TenantID,
		tenantWithAccount.Accounts[Checking].AccountID,
		tenantWithAccount.Accounts[Savings].AccountID,
		*new(big.Rat).SetFloat64(amount),
		uuid.New().String())
	if err != nil {
		t.Fatalf("Failed to transfer funds, err: %v", err)
	}

	checking, err := models.FindAccount(t.Context(), client.Single(),
		tenantWithAccount.Tenant.TenantID,
		tenantWithAccount.Accounts[Checking].AccountID)
	if err != nil {
		t.Fatalf("Failed to find checking account, err: %v", err)
	}
	assert.NotNil(t, checking)
	balance, _ := checking.Balance.Float64()
	assert.Equal(t, balance, DefaultCheckingBalance-amount)

	savings, err := models.FindAccount(t.Context(), client.Single(),
		tenantWithAccount.Tenant.TenantID,
		tenantWithAccount.Accounts[Savings].AccountID)
	if err != nil {
		t.Fatalf("Failed to find savings account, err: %v", err)
	}
	assert.NotNil(t, savings)
	balance, _ = savings.Balance.Float64()
	assert.Equal(t, balance, DefaultSavingsBalance+amount)

	checkingTransactions, err := GetAccountTransactions(t.Context(),
		client, tenantWithAccount.Tenant.TenantID, checking.AccountID)
	if err != nil {
		t.Fatalf("Failed to get checking transactions, err: %v", err)
	}
	assert.NotNil(t, checkingTransactions)
	assert.Equal(t, len(checkingTransactions), 1)
	assert.Equal(t, checkingTransactions[0].TransactionType,
		Debit.String())
	transAmount, _ := checkingTransactions[0].Amount.Float64()
	assert.Equal(t, transAmount, amount)

	savingsTransactions, err := GetAccountTransactions(t.Context(),
		client, tenantWithAccount.Tenant.TenantID, savings.AccountID)
	if err != nil {
		t.Fatalf("Failed to savings transactions, err: %v", err)
	}
	assert.NotNil(t, savingsTransactions)
	assert.Equal(t, len(savingsTransactions), 1)
	assert.Equal(t, savingsTransactions[0].TransactionType,
		Credit.String())
	transAmount, _ = savingsTransactions[0].Amount.Float64()
	assert.Equal(t, transAmount, amount)

	_, err = TransferFunds(t.Context(), client,
		tenantWithAccount.Tenant.TenantID,
		tenantWithAccount.Accounts[Checking].AccountID,
		tenantWithAccount.Accounts[Savings].AccountID,
		*new(big.Rat).SetFloat64(1000.0),
		uuid.New().String())
	assert.NotNil(t, err)
}
