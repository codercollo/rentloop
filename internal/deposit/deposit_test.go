package deposit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/codercollo/rentloop/internal/deposit"
	"github.com/codercollo/rentloop/internal/models"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ------------------- Mocks -------------------

type MockRepo struct{ mock.Mock }

func (m *MockRepo) GetByUnit(ctx context.Context, unitID string) (*models.Deposit, error) {
	args := m.Called(ctx, unitID)
	if dep := args.Get(0); dep != nil {
		return dep.(*models.Deposit), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockRepo) Upsert(ctx context.Context, d models.Deposit) (*models.Deposit, error) {
	args := m.Called(ctx, d)
	if dep := args.Get(0); dep != nil {
		return dep.(*models.Deposit), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockRepo) InsertTransaction(ctx context.Context, t models.DepositTransaction) (*models.DepositTransaction, error) {
	args := m.Called(ctx, t)
	if txn := args.Get(0); txn != nil {
		return txn.(*models.DepositTransaction), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockRepo) GetTransactions(ctx context.Context, depositID string, limit int) ([]models.DepositTransaction, error) {
	args := m.Called(ctx, depositID, limit)
	return args.Get(0).([]models.DepositTransaction), args.Error(1)
}

type MockUnitFetcher struct{ mock.Mock }

func (m *MockUnitFetcher) GetUnitByRef(ctx context.Context, landlordID, ref string) (*models.Unit, error) {
	args := m.Called(ctx, landlordID, ref)
	if u := args.Get(0); u != nil {
		return u.(*models.Unit), args.Error(1)
	}
	return nil, args.Error(1)
}

// ------------------- Tests -------------------

func TestDepositService_Get(t *testing.T) {
	ctx := context.Background()
	repo := new(MockRepo)
	fetcher := new(MockUnitFetcher)
	svc := deposit.NewService(repo, fetcher)

	unit := &models.Unit{ID: "unit1", UnitRef: "U1"}

	// 1. Unit fetch succeeds, no deposit exists yet
	fetcher.On("GetUnitByRef", ctx, "l1", "U1").Return(unit, nil)
	repo.On("GetByUnit", ctx, "unit1").Return(nil, models.ErrNotFound)

	res, err := svc.Get(ctx, "l1", "U1")
	require.NoError(t, err)
	require.NotNil(t, res.Deposit)
	require.Equal(t, "unit1", res.Deposit.UnitID)

	// 2. Unit fetch fails
	fetcher.On("GetUnitByRef", ctx, "l1", "BAD").Return(nil, errors.New("not found"))
	_, err = svc.Get(ctx, "l1", "BAD")
	require.Error(t, err)
	require.Contains(t, err.Error(), "resolve unit")
}

func TestDepositService_Receive(t *testing.T) {
	ctx := context.Background()
	repo := new(MockRepo)
	fetcher := new(MockUnitFetcher)
	svc := deposit.NewService(repo, fetcher)

	unit := &models.Unit{ID: "unit1", UnitRef: "U1"}

	// Setup common fetcher response
	fetcher.On("GetUnitByRef", ctx, "l1", "U1").Return(unit, nil)

	t.Run("creates new deposit if none exists", func(t *testing.T) {
		repo.On("GetByUnit", ctx, "unit1").Return(nil, models.ErrNotFound)
		repo.On("Upsert", ctx, mock.Anything).Return(&models.Deposit{
			ID:              "dep1",
			UnitID:          "unit1",
			LandlordID:      "l1",
			DepositExpected: 1000,
			DepositPaid:     1000,
			DepositBalance:  0,
		}, nil)
		repo.On("InsertTransaction", ctx, mock.Anything).Return(&models.DepositTransaction{ID: "txn1"}, nil)

		res, err := svc.Receive(ctx, "l1", "U1", 1000, "initial deposit", "landlord")
		require.NoError(t, err)
		require.Equal(t, 1000, res.Deposit.DepositPaid)
		require.Len(t, res.Transactions, 1)
	})

	t.Run("fails on non-positive amount", func(t *testing.T) {
		_, err := svc.Receive(ctx, "l1", "U1", 0, "note", "landlord")
		require.Error(t, err)
		require.Contains(t, err.Error(), "amount must be positive")
	})
}

func TestDepositService_Refund(t *testing.T) {
	ctx := context.Background()
	repo := new(MockRepo)
	fetcher := new(MockUnitFetcher)
	svc := deposit.NewService(repo, fetcher)

	unit := &models.Unit{ID: "unit1", UnitRef: "U1"}
	dep := &models.Deposit{
		ID:              "dep1",
		UnitID:          "unit1",
		LandlordID:      "l1",
		DepositExpected: 1000,
		DepositPaid:     1000,
		DepositRefunded: 200,
		DepositBalance:  0,
	}

	// Setup common fetcher
	fetcher.On("GetUnitByRef", ctx, "l1", "U1").Return(unit, nil)
	repo.On("GetByUnit", ctx, "unit1").Return(dep, nil)
	repo.On("Upsert", ctx, mock.Anything).Return(dep, nil)
	repo.On("InsertTransaction", ctx, mock.Anything).Return(&models.DepositTransaction{ID: "txn1"}, nil)

	t.Run("successful refund", func(t *testing.T) {
		res, err := svc.Refund(ctx, "l1", "U1", 300, "partial refund", "landlord")
		require.NoError(t, err)

		// The deposit state is updated correctly
		require.Equal(t, 500, dep.DepositRefunded)
		require.Equal(t, 0, dep.DepositBalance)

		// One transaction was recorded
		require.Len(t, res.Transactions, 1)
	})

	t.Run("refund exceeds balance", func(t *testing.T) {
		_, err := svc.Refund(ctx, "l1", "U1", 1000, "too much", "landlord")
		require.Error(t, err)
		require.Contains(t, err.Error(), "exceeds refundable balance")
	})

	t.Run("non-positive refund", func(t *testing.T) {
		_, err := svc.Refund(ctx, "l1", "U1", 0, "invalid", "landlord")
		require.Error(t, err)
		require.Contains(t, err.Error(), "amount must be positive")
	})
}
