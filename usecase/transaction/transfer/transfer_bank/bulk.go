package transfer_bank

import (
	"fmt"

	"github.com/takeme-id/core/domain"
	"github.com/takeme-id/core/service"
	"github.com/takeme-id/core/usecase"
	"github.com/takeme-id/core/utils"
)

func CreateBulkInquiry(corporate domain.Corporate, reference string, banks []domain.Bank,
	actor domain.ActorObject) (domain.BulkInquiry, error) {

	totalBulk := len(banks)
	if totalBulk == 0 {
		return domain.BulkInquiry{}, utils.ErrorBadRequest(utils.BulkListEmpty, "Bulk list empty")
	}

	bulk := service.CreateBulkInquiry(corporate, totalBulk, reference, banks, actor)

	err := service.SaveBulkInquiry(&bulk)
	if err != nil {
		return domain.BulkInquiry{}, err
	}

	go executeBulkInquiry(corporate, actor, bulk)

	return bulk, nil
}

func CreateBulkTransfer(corporate domain.Corporate, reference string, transfers []domain.Transfer,
	actor domain.ActorObject, balanceID string) (domain.BulkTransfer, error) {

	totalBulk := len(transfers)
	if totalBulk == 0 {
		return domain.BulkTransfer{}, utils.ErrorBadRequest(utils.BulkListEmpty, "Bulk list empty")
	}

	balance, err := service.BalanceByIDNoSession(balanceID)
	if err != nil || balance.Owner.Type == "" {
		return domain.BulkTransfer{}, utils.ErrorBadRequest(utils.InvalidBalanceID, "Balance not found")
	}

	bulk, err := service.CreateBulkTransfer(corporate, totalBulk, reference, transfers, actor, balance)
	if err != nil {
		return domain.BulkTransfer{}, err
	}

	err = service.SaveBulkTransfer(&bulk)
	if err != nil {
		return domain.BulkTransfer{}, err
	}

	return bulk, nil
}

func ActorExecuteBulkTransfer(corporate domain.Corporate, user domain.ActorAble, pin string,
	bulkID string) (domain.BulkTransfer, error) {

	bulk, err := service.BulkTransferByID(bulkID)
	if err != nil || bulk.Time == "" || bulk.Status != domain.BULK_UNEXECUTED_STATUS {
		return domain.BulkTransfer{}, utils.ErrorBadRequest(utils.BulkNotFound, "Bulk Not found or already executed")
	}

	err = usecase.ValidateActorPIN(user, pin)
	if err != nil {
		return domain.BulkTransfer{}, err
	}

	err = usecase.ValidateAccessBalance(user, bulk.BalanceID.Hex())
	if err != nil {
		return domain.BulkTransfer{}, err
	}

	err = usecase.ValidateIsVerify(user)
	if err != nil {
		return domain.BulkTransfer{}, err
	}

	go executeBulkTransfer(corporate, user, pin, bulk)

	bulk.Status = domain.BULK_PROGRESS_STATUS
	return bulk, nil
}

func ViewBulkInquiry(bulkID string) (domain.BulkInquiry, error) {

	bulk, err := service.BulkInquiryByID(bulkID)
	if err != nil {
		return domain.BulkInquiry{}, err
	}

	return bulk, nil
}

func ViewBulkTransfer(bulkID string) (domain.BulkTransfer, error) {

	bulk, err := service.BulkTransferByID(bulkID)
	if err != nil {
		return domain.BulkTransfer{}, err
	}

	return bulk, nil
}

func executeBulkInquiry(corporate domain.Corporate, actor domain.ActorObject, bulk domain.BulkInquiry) {
	var result []domain.Inquiry
	for _, inq := range bulk.List {

		var a domain.Inquiry

		bank, err := InquiryBankAccount(inq.AccountNumber, inq.BankName)
		if err != nil {
			a.AccountName = inq.AccountName
			a.AccountNumber = inq.AccountNumber
			a.BankName = inq.BankName
			a.Number = inq.Number
			a.Valid = false
			if err.Error() != "" {
				a.Reason = err.Error()
			} else {
				a.Reason = "Invalid bank code or account number empty"
			}
			result = append(result, a)
		} else {
			a.AccountName = bank.Name
			a.AccountNumber = inq.AccountNumber
			a.BankName = inq.BankName
			a.Number = inq.Number
			a.Valid = true
			a.Reason = ""
			result = append(result, a)
		}
	}

	bulk.List = result
	bulk.Status = domain.BULK_COMPLETED_STATUS
	go service.BulkInquiryUpdateOne(&bulk)

	go usecase.PublishBulkCallback(corporate, actor, bulk.ID.Hex(), bulk.Status, corporate.BulkInquiryCallbackURL)
}

func executeBulkTransfer(corporate domain.Corporate, user domain.ActorAble, pin string, bulk domain.BulkTransfer) {

	bulk.Status = domain.BULK_PROGRESS_STATUS
	service.BulkTransferUpdateOne(&bulk)

	transfers := bulk.List
	for index, transfer := range transfers {
		usecase := UserTransferBank{}
		trx, err := usecase.Execute(corporate, user, transfer.ToBankAccount.ToTransactionObject(),
			bulk.BalanceID.Hex(), transfer.Amount, pin, transfer.ExternalID)
		if err != nil {
			err, ok := err.(utils.CustomError)
			if !ok {
				bulk.List[index].Reason = "Internal server error"
				continue
			}

			bulk.List[index].Reason = utils.ResponseDescription[fmt.Sprintf("%v.%v", err.Code, "en")]
			bulk.FailedNumber = append(bulk.FailedNumber, transfer.Number)
			continue
		}

		bulk.List[index].TransactionCode = trx.TransactionCode
	}

	bulk.Status = domain.BULK_COMPLETED_STATUS
	service.BulkTransferUpdateOne(&bulk)
	go usecase.PublishBulkCallback(corporate, bulk.Owner, bulk.ID.Hex(), bulk.Status, corporate.BulkTransferCallbackURL)
}
