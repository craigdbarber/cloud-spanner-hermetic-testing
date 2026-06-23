CREATE TABLE IF NOT EXISTS Tenants (
    TenantID    STRING(36) NOT NULL,
    Name        STRING(256) NOT NULL
) PRIMARY KEY(TenantID);

CREATE TABLE IF NOT EXISTS Accounts (
    TenantID    STRING(36) NOT NULL,
    AccountID   STRING(36) NOT NULL,
    Name        STRING(256) NOT NULL,
    Balance     NUMERIC NOT NULL
) PRIMARY KEY(TenantID, AccountID),
INTERLEAVE IN PARENT Tenants ON DELETE CASCADE;

CREATE TABLE IF NOT EXISTS Transactions (
    TenantID        STRING(36) NOT NULL,
    AccountID       STRING(36) NOT NULL,
    TransactionID   STRING(36) NOT NULL,
    Amount          NUMERIC NOT NULL,
    CreatedAt       TIMESTAMP NOT NULL,
    TransactionType STRING(10) NOT NULL,
    ReferenceID     STRING(36) NOT NULL DEFAULT (GENERATE_UUID())
) PRIMARY KEY(TenantID, AccountID, TransactionID),
INTERLEAVE IN PARENT Accounts ON DELETE CASCADE;
CREATE INDEX TransactionsByReferenceID ON Transactions(ReferenceID) STORING (Amount, TransactionType);