package transaction

import (
	"context"

	"github.com/takeme-id/core/domain"
	"github.com/takeme-id/core/service"
	"github.com/takeme-id/core/usecase"
	"github.com/takeme-id/core/utils"
	"github.com/takeme-id/core/utils/database"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

type Base struct {
}

func (self Base) CreateFeeStatement(corporate domain.Corporate, balance domain.Balance,
	transaction domain.Transaction) ([]domain.Statement, error) {
	feeCalculator := usecase.CalculateFee{}
	feeCalculator.Initialize(corporate, balance, transaction)

	statements, err := feeCalculator.CalculateByOwnerAndTransaction()
	if err != nil {
		return []domain.Statement{}, err
	}

	return statements, nil
}

func (self Base) RollbackFeeStatement(corporate domain.Corporate, balance domain.Balance,
	transaction domain.Transaction) ([]domain.Statement, error) {
	feeCalculator := usecase.CalculateFee{}
	feeCalculator.Initialize(corporate, balance, transaction)

	feeStatements, err := feeCalculator.CalculateByOwnerAndTransaction()
	statements := feeCalculator.RollbackFeeStatement(feeStatements)

	if err != nil {
		return []domain.Statement{}, err
	}

	return statements, nil
}

func (self Base) Commit(statements []domain.Statement, transaction *domain.Transaction) error {
	function := func(session mongo.SessionContext) error {
		err := session.StartTransaction(options.Transaction().
			SetReadConcern(readconcern.Snapshot()).
			SetWriteConcern(writeconcern.New(writeconcern.WMajority())),
		)

		if err != nil {
			session.AbortTransaction(session)
			return utils.ErrorInternalServer(utils.DBStartTransactionFailed, "Initialize balance start transaction failed")
		}

		err = adjustBalanceWithStatement(statements, session)
		if err != nil {
			session.AbortTransaction(session)
			return err
		}

		err = service.TransactionSaveOne(transaction, session)
		if err != nil {
			session.AbortTransaction(session)
			return err
		}

		return database.CommitWithRetry(session)

	}

	err := database.DBClient.UseSessionWithOptions(
		context.TODO(), options.Session().SetDefaultReadPreference(readpref.Primary()),
		func(sctx mongo.SessionContext) error {
			return database.RunTransactionWithRetry(sctx, function)
		},
	)

	if err != nil {
		return err
	}

	return nil
}

func (self Base) CommitRollback(statements []domain.Statement) error {
	function := func(session mongo.SessionContext) error {
		err := session.StartTransaction(options.Transaction().
			SetReadConcern(readconcern.Snapshot()).
			SetWriteConcern(writeconcern.New(writeconcern.WMajority())),
		)

		if err != nil {
			session.AbortTransaction(session)
			return utils.ErrorInternalServer(utils.DBStartTransactionFailed, "Initialize balance start transaction failed")
		}

		err = adjustBalanceWithStatement(statements, session)
		if err != nil {
			session.AbortTransaction(session)
			return err
		}

		return database.CommitWithRetry(session)

	}

	err := database.DBClient.UseSessionWithOptions(
		context.TODO(), options.Session().SetDefaultReadPreference(readpref.Primary()),
		func(sctx mongo.SessionContext) error {
			return database.RunTransactionWithRetry(sctx, function)
		},
	)

	if err != nil {
		return err
	}

	return nil
}

func adjustBalanceWithStatement(statements []domain.Statement, session mongo.SessionContext) error {

	for _, statement := range statements {
		if statement.Deposit != 0 {
			err := usecase.DepositBalance(statement, session)
			if err != nil {
				return err
			}
		} else if statement.Withdraw != 0 {
			err := usecase.WithdrawBalance(statement, session)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
