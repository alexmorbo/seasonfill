package regrab

//go:generate mockgen -destination=mocks/qbit_mock.go -package=mocks github.com/alexmorbo/seasonfill/internal/shared/clients/qbit Client
//go:generate mockgen -destination=mocks/grab_repository_mock.go -package=mocks github.com/alexmorbo/seasonfill/internal/shared/dataports GrabRepository,CooldownRepository,WatchdogBlacklistRepository,WatchdogStateRepository,DecisionRepository
