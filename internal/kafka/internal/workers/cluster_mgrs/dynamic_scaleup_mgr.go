package cluster_mgrs

import (
	"github.com/bf2fc6cc711aee1a0c2a/kas-fleet-manager/internal/kafka/internal/config"
	"github.com/bf2fc6cc711aee1a0c2a/kas-fleet-manager/internal/kafka/internal/services"
	fleeterrors "github.com/bf2fc6cc711aee1a0c2a/kas-fleet-manager/pkg/errors"
	"github.com/golang/glog"
	"github.com/pkg/errors"

	"github.com/bf2fc6cc711aee1a0c2a/kas-fleet-manager/pkg/api"
	"github.com/bf2fc6cc711aee1a0c2a/kas-fleet-manager/pkg/shared/utils/arrays"
	"github.com/bf2fc6cc711aee1a0c2a/kas-fleet-manager/pkg/workers"
	"github.com/google/uuid"
)

const (
	DynamicScaleUpWorkerType = "dynamic_scale_up"
)

type DynamicScaleUpManager struct {
	workers.BaseWorker

	DataplaneClusterConfig *config.DataplaneClusterConfig
	ClusterProvidersConfig *config.ProviderConfig
	KafkaConfig            *config.KafkaConfig

	ClusterService services.ClusterService
}

var _ workers.Worker = &DynamicScaleUpManager{}

func NewDynamicScaleUpManager(
	reconciler workers.Reconciler,
	dataplaneClusterConfig *config.DataplaneClusterConfig,
	clusterProvidersConfig *config.ProviderConfig,
	kafkaConfig *config.KafkaConfig,
	clusterService services.ClusterService,
) *DynamicScaleUpManager {

	return &DynamicScaleUpManager{
		BaseWorker: workers.BaseWorker{
			Id:         uuid.New().String(),
			WorkerType: DynamicScaleUpWorkerType,
			Reconciler: reconciler,
		},

		DataplaneClusterConfig: dataplaneClusterConfig,
		ClusterProvidersConfig: clusterProvidersConfig,
		KafkaConfig:            kafkaConfig,

		ClusterService: clusterService,
	}

}

func (m *DynamicScaleUpManager) Start() {
	m.StartWorker(m)
}

func (m *DynamicScaleUpManager) Stop() {
	m.StopWorker(m)
}

func (m *DynamicScaleUpManager) Reconcile() []error {
	var errList fleeterrors.ErrorList
	if !m.DataplaneClusterConfig.IsDataPlaneAutoScalingEnabled() {
		glog.Infoln("dynamic scaling is disabled. Dynamic scale up reconcile event skipped")
		return nil
	}
	glog.Infoln("running dynamic scale up reconcile event")
	defer m.logReconcileEventEnd()

	// TODO remove this method call and the method itself the new dynamic scale up
	// logic is ready
	err := m.reconcileClustersForRegions()
	if err != nil {
		errList.AddErrors(err...)
	}

	// TODO remove the "if false" condition once the new dynamic scale up
	// logic is ready
	if false {
		err := m.processDynamicScaleUpReconcileEvent()
		if err != nil {
			errList.AddErrors(err)
		}
	}

	return errList.ToErrorSlice()
}

// reconcileClustersForRegions creates an OSD cluster for each supported cloud provider and region where no cluster exists.
func (m *DynamicScaleUpManager) reconcileClustersForRegions() []error {
	var errs []error
	glog.Infoln("reconcile cloud providers and regions")
	var providers []string
	var regions []string
	status := api.StatusForValidCluster
	//gather the supported providers and regions
	providerList := m.ClusterProvidersConfig.ProvidersConfig.SupportedProviders
	for _, v := range providerList {
		providers = append(providers, v.Name)
		for _, r := range v.Regions {
			regions = append(regions, r.Name)
		}
	}

	// get a list of clusters in Map group by their provider and region.
	grpResult, err := m.ClusterService.ListGroupByProviderAndRegion(providers, regions, status)
	if err != nil {
		errs = append(errs, errors.Wrapf(err, "failed to find cluster with criteria"))
		return errs
	}

	grpResultMap := make(map[string]*services.ResGroupCPRegion)
	for _, v := range grpResult {
		grpResultMap[v.Provider+"."+v.Region] = v
	}

	// create all the missing clusters in the supported provider and regions.
	for _, p := range providerList {
		for _, v := range p.Regions {
			if _, exist := grpResultMap[p.Name+"."+v.Name]; !exist {
				clusterRequest := api.Cluster{
					CloudProvider:         p.Name,
					Region:                v.Name,
					MultiAZ:               true,
					Status:                api.ClusterAccepted,
					ProviderType:          api.ClusterProviderOCM,
					SupportedInstanceType: api.AllInstanceTypeSupport.String(), // TODO - make sure we use the appropriate instance type.
				}
				if err := m.ClusterService.RegisterClusterJob(&clusterRequest); err != nil {
					errs = append(errs, errors.Wrapf(err, "Failed to auto-create cluster request in %s, region: %s", p.Name, v.Name))
					return errs
				} else {
					glog.Infof("Auto-created cluster request in %s, region: %s, Id: %s ", p.Name, v.Name, clusterRequest.ID)
				}
			} //
		} //region
	} //provider
	return errs
}

func (m *DynamicScaleUpManager) processDynamicScaleUpReconcileEvent() error {
	var errList fleeterrors.ErrorList
	kafkaStreamingUnitCountPerClusterList, err := m.ClusterService.FindStreamingUnitCountByClusterAndInstanceType()
	if err != nil {
		errList.AddErrors(err)
		return errList
	}

	for _, provider := range m.ClusterProvidersConfig.ProvidersConfig.SupportedProviders {
		for _, region := range provider.Regions {
			for supportedInstanceTypeName := range region.SupportedInstanceTypes {
				supportedInstanceTypeConfig := region.SupportedInstanceTypes[supportedInstanceTypeName]
				var dynamicScaleUpProcessor dynamicScaleUpProcessor = &standardDynamicScaleUpProcessor{
					locator: supportedInstanceTypeLocator{
						provider:         provider.Name,
						region:           region.Name,
						instanceTypeName: supportedInstanceTypeName,
					},
					instanceTypeConfig:                    &supportedInstanceTypeConfig,
					kafkaStreamingUnitCountPerClusterList: kafkaStreamingUnitCountPerClusterList,
					supportedKafkaInstanceTypesConfig:     &m.KafkaConfig.SupportedInstanceTypes.Configuration,
					clusterService:                        m.ClusterService,
					dryRun:                                false,
				}

				shouldScaleUp, err := dynamicScaleUpProcessor.ShouldScaleUp()
				if err != nil {
					errList.AddErrors(err)
					continue
				}
				if shouldScaleUp {
					err := dynamicScaleUpProcessor.ScaleUp()
					if err != nil {
						errList.AddErrors(err)
						continue
					}
				}
			}
		}
	}

	return nil
}

func (m *DynamicScaleUpManager) logReconcileEventEnd() {
	glog.Infoln("dynamic scale up reconcile event finished")
}

// supportedInstanceTypeLocator is a data structure
// that contains all the information to help locate a
// supported instance type in a region's cluster
type supportedInstanceTypeLocator struct {
	provider         string
	region           string
	instanceTypeName string
}

func (l *supportedInstanceTypeLocator) Equal(other supportedInstanceTypeLocator) bool {
	return l.provider == other.provider &&
		l.region == other.region &&
		l.instanceTypeName == other.instanceTypeName
}

// dynamicScaleUpExecutor is able to perform dynamic ScaleUp execution actions
type dynamicScaleUpExecutor interface {
	ScaleUp() error
}

// dynamicScaleUpEvaluator is able to perform dynamic ScaleUp evaluation actions
type dynamicScaleUpEvaluator interface {
	ShouldScaleUp() (bool, error)
}

// dynamicScaleUpProcessor is able to process dynamic ScaleUp reconcile events
type dynamicScaleUpProcessor interface {
	dynamicScaleUpExecutor
	dynamicScaleUpEvaluator
}

// noopDynamicScaleUpProcessor is a dynamicScaleUpProcessor where
// the scale up is a noop and it always returns that no scale up action
// is needed
type noopDynamicScaleUpProcessor struct {
}

var _ dynamicScaleUpEvaluator = &noopDynamicScaleUpProcessor{}
var _ dynamicScaleUpExecutor = &noopDynamicScaleUpProcessor{}
var _ dynamicScaleUpProcessor = &noopDynamicScaleUpProcessor{}

func (p *noopDynamicScaleUpProcessor) ScaleUp() error {
	return nil
}

func (p *noopDynamicScaleUpProcessor) ShouldScaleUp() (bool, error) {
	return false, nil
}

// standardDynamicScaleUpProcessor is the default dynamicScaleUpProcessor
// used when dynamic scaling is enabled.
// It assumes the provided kafkaStreamingUnitCountPerClusterList does not
// contain any element with a Status attribute with value 'failed'
type standardDynamicScaleUpProcessor struct {
	locator            supportedInstanceTypeLocator
	instanceTypeConfig *config.InstanceTypeConfig
	// kafkaStreamingUnitCountPerClusterList must not contain any element
	// with a Status attribute with value 'failed'
	kafkaStreamingUnitCountPerClusterList services.KafkaStreamingUnitCountPerClusterList
	supportedKafkaInstanceTypesConfig     *config.SupportedKafkaInstanceTypesConfig
	clusterService                        services.ClusterService

	// dryRun controls whether the ScaleUp method performs real actions.
	// Useful when you don't want to trigger a real scale up.
	dryRun bool
}

var _ dynamicScaleUpEvaluator = &standardDynamicScaleUpProcessor{}
var _ dynamicScaleUpExecutor = &standardDynamicScaleUpProcessor{}
var _ dynamicScaleUpProcessor = &standardDynamicScaleUpProcessor{}

// ShouldScaleUp indicates whether a new data plane
// cluster should be created for a given instance type in the given provider
// and region.
// It returns true if all the following conditions happen:
// 1. If specified, the streaming units limit for the given instance type in
//    the provider's region has not been reached
// 2. There is no scale up action ongoing. A scale up action is ongoing
//    if there is at least one cluster in the following states: 'provisioning',
//    'provisioned', 'accepted', 'waiting_for_kas_fleetshard_operator'
// 3. At least one of the two following conditions are true:
//      * No cluster in the provider's region has enough capacity to allocate
//        the biggest instance size of the given instance type
//      * The free capacity (in streaming units) for the given instance type in
//        the provider's region is smaller or equal than the defined slack
//        capacity (also in streaming units) of the given instance type. Free
//        capacity is defined as max(total) capacity - consumed capacity.
//        For the calculation of the max capacity:
//        * Clusters in deprovisioning and cleanup state are excluded, as
//          clusters into those states don't accept kafka instances anymore.
//        * Clusters that are still not ready to accept kafka instance but that
//          should eventually accept them (like accepted state for example)
//          are included
// Otherwise false is returned.
// Note: This method assumes kafkaStreamingUnitCountPerClusterList does not
//       contain elements with the Status attribute with the 'failed' value.
//       Thus, if the type is constructed with the assumptions being true, it
//       can be considered as the 'failed' state Clusters are not included in
//       the calculations.
func (p *standardDynamicScaleUpProcessor) ShouldScaleUp() (bool, error) {
	summaryCalculator := instanceTypeConsumptionSummaryCalculator{
		locator:                               p.locator,
		kafkaStreamingUnitCountPerClusterList: p.kafkaStreamingUnitCountPerClusterList,
		supportedKafkaInstanceTypesConfig:     p.supportedKafkaInstanceTypesConfig,
	}

	instanceTypeConsumptionInRegionSummary, err := summaryCalculator.Calculate()
	if err != nil {
		return false, err
	}

	regionLimitReached := p.regionLimitReached(instanceTypeConsumptionInRegionSummary)
	if regionLimitReached {
		return false, nil
	}

	ongoingScaleUpActionInRegion := p.ongoingScaleUpAction(instanceTypeConsumptionInRegionSummary)
	if ongoingScaleUpActionInRegion {
		return false, nil
	}

	freeCapacityForBiggestInstanceSizeInRegion := p.freeCapacityForBiggestInstanceSize(instanceTypeConsumptionInRegionSummary)
	if !freeCapacityForBiggestInstanceSizeInRegion {
		return true, nil
	}

	enoughCapacitySlackInRegion := p.enoughCapacitySlackInRegion(instanceTypeConsumptionInRegionSummary)
	if !enoughCapacitySlackInRegion {
		return true, nil
	}

	return false, nil
}

// ScaleUp triggers a new data plane cluster
// registration for a given instance type in a provider region.
func (p *standardDynamicScaleUpProcessor) ScaleUp() error {
	if p.dryRun {
		return nil
	}

	// If the provided instance type to support is standard the new cluster
	// to register will be MultiAZ. Otherwise will be single AZ
	newClusterMultiAZ := p.locator.instanceTypeName == api.StandardTypeSupport.String()

	clusterRequest := &api.Cluster{
		CloudProvider:         p.locator.provider,
		Region:                p.locator.region,
		SupportedInstanceType: p.locator.instanceTypeName,
		MultiAZ:               newClusterMultiAZ,
		Status:                api.ClusterAccepted,
		ProviderType:          api.ClusterProviderOCM,
	}

	err := p.clusterService.RegisterClusterJob(clusterRequest)
	if err != nil {
		return err
	}

	return nil
}

func (p *standardDynamicScaleUpProcessor) enoughCapacitySlackInRegion(summary instanceTypeConsumptionSummary) bool {
	freeStreamingUnitsInRegion := summary.freeStreamingUnits
	capacitySlackInRegion := p.instanceTypeConfig.MinAvailableCapacitySlackStreamingUnits

	// Note: if capacitySlackInRegion is 0 we always return that there is enough
	// capacity slack in region.
	return freeStreamingUnitsInRegion >= capacitySlackInRegion
}

func (p *standardDynamicScaleUpProcessor) regionLimitReached(summary instanceTypeConsumptionSummary) bool {
	streamingUnitsLimitInRegion := p.instanceTypeConfig.Limit
	consumedStreamingUnitsInRegion := summary.consumedStreamingUnits

	if streamingUnitsLimitInRegion == nil {
		return false
	}

	return consumedStreamingUnitsInRegion >= *streamingUnitsLimitInRegion
}

func (p *standardDynamicScaleUpProcessor) freeCapacityForBiggestInstanceSize(summary instanceTypeConsumptionSummary) bool {
	return summary.biggestInstanceSizeCapacityAvailable
}

func (p *standardDynamicScaleUpProcessor) ongoingScaleUpAction(summary instanceTypeConsumptionSummary) bool {
	return summary.ongoingScaleUpAction
}

// instanceTypeConsumptionSummary contains a consumption summary
// of an instance in a provider's region
type instanceTypeConsumptionSummary struct {
	// maxStreamingUnits is the total capacity for the instance type in
	// streaming units
	maxStreamingUnits int
	// freeStreamingUnits is the free capacity for the instance type in
	// streaming units
	freeStreamingUnits int
	// consumedStreamingUnits is the consumed capacity for the instance type in
	// streaming units
	consumedStreamingUnits int
	// ongoingScaleUpAction designates whether a data plane cluster in the
	// provider's region, which supports the instance type, is being created
	ongoingScaleUpAction bool
	// biggestInstanceSizeCapacityAvailable indicates whether there is capacity
	// to allocate at least one unit of the biggest kafka instance size of
	// the instance type
	biggestInstanceSizeCapacityAvailable bool
}

// instanceTypeConsumptionSummaryCalculator calculates a consumption summary
// for the provided supportedInstanceTypeLocator based on the
// provided KafkaStreamingUnitCountPerClusterList
type instanceTypeConsumptionSummaryCalculator struct {
	locator                               supportedInstanceTypeLocator
	kafkaStreamingUnitCountPerClusterList services.KafkaStreamingUnitCountPerClusterList
	supportedKafkaInstanceTypesConfig     *config.SupportedKafkaInstanceTypesConfig
}

// Calculate returns a instanceTypeConsumptionSummary containing a consumption
// summary for the provided supportedInstanceTypeLocator
// For the calculation of the max streaming units capacity:
//   * Clusters in deprovisioning and cleanup state are excluded, as
//     clusters into those states don't accept kafka instances anymore.
//   * Clusters that are still not ready to accept kafka instance but that
//     should eventually accept them (like accepted state for example)
//     are included
// For the calculation of whether a scale up actions is ongoing:
//   * A scale up action is ongoing if there is at least one cluster in the
//     following states: 'provisioning', 'provisioned', 'accepted',
//     'waiting_for_kas_fleetshard_operator'
func (i *instanceTypeConsumptionSummaryCalculator) Calculate() (instanceTypeConsumptionSummary, error) {
	biggestKafkaInstanceSizeCapacityConsumption, err := i.getBiggestCapacityConsumedSize()
	if err != nil {
		return instanceTypeConsumptionSummary{}, err
	}

	consumedStreamingUnitsInRegion := 0
	maxStreamingUnitsInRegion := 0
	atLeastOneClusterHasCapacityForBiggestInstanceType := false
	scaleUpActionIsOngoing := false

	clusterStatesTowardReadyState := []string{
		api.ClusterProvisioning.String(), api.ClusterProvisioned.String(),
		api.ClusterAccepted.String(), api.ClusterWaitingForKasFleetShardOperator.String(),
	}
	clusterStatesTowardDeletion := []string{api.ClusterDeprovisioning.String(), api.ClusterCleanup.String()}

	for _, kafkaStreamingUnitCountPerCluster := range i.kafkaStreamingUnitCountPerClusterList {
		currLocator := supportedInstanceTypeLocator{
			provider:         kafkaStreamingUnitCountPerCluster.CloudProvider,
			region:           kafkaStreamingUnitCountPerCluster.Region,
			instanceTypeName: kafkaStreamingUnitCountPerCluster.InstanceType,
		}

		if !i.locator.Equal(currLocator) {
			continue
		}

		if arrays.Contains(clusterStatesTowardReadyState, kafkaStreamingUnitCountPerCluster.Status) {
			scaleUpActionIsOngoing = true
		}

		if kafkaStreamingUnitCountPerCluster.FreeStreamingUnits() >= int32(biggestKafkaInstanceSizeCapacityConsumption) {
			atLeastOneClusterHasCapacityForBiggestInstanceType = true
		}

		consumedStreamingUnitsInRegion = consumedStreamingUnitsInRegion + int(kafkaStreamingUnitCountPerCluster.Count)
		if !arrays.Contains(clusterStatesTowardDeletion, kafkaStreamingUnitCountPerCluster.Status) {
			maxStreamingUnitsInRegion = maxStreamingUnitsInRegion + int(kafkaStreamingUnitCountPerCluster.MaxUnits)
		}
	}

	freeStreamingUnitsInRegion := maxStreamingUnitsInRegion - consumedStreamingUnitsInRegion

	return instanceTypeConsumptionSummary{
		maxStreamingUnits:                    maxStreamingUnitsInRegion,
		consumedStreamingUnits:               consumedStreamingUnitsInRegion,
		freeStreamingUnits:                   freeStreamingUnitsInRegion,
		ongoingScaleUpAction:                 scaleUpActionIsOngoing,
		biggestInstanceSizeCapacityAvailable: atLeastOneClusterHasCapacityForBiggestInstanceType,
	}, nil
}

func (i *instanceTypeConsumptionSummaryCalculator) getBiggestCapacityConsumedSize() (int, error) {
	kafkaInstanceTypeConfig, err := i.supportedKafkaInstanceTypesConfig.GetKafkaInstanceTypeByID(i.locator.instanceTypeName)
	if err != nil {
		return -1, err
	}
	biggestKafkaInstanceSizeCapacityConsumption := -1
	maxKafkaInstanceSizeConfig := kafkaInstanceTypeConfig.GetBiggestCapacityConsumedSize()
	if maxKafkaInstanceSizeConfig != nil {
		biggestKafkaInstanceSizeCapacityConsumption = maxKafkaInstanceSizeConfig.CapacityConsumed
	}
	return biggestKafkaInstanceSizeCapacityConsumption, nil
}
