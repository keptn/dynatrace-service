package event_handler

import (
	"errors"
	"fmt"

	keptncommon "github.com/keptn/go-utils/pkg/lib/keptn"
	keptnv2 "github.com/keptn/go-utils/pkg/lib/v0_2_0"
	log "github.com/sirupsen/logrus"

	"github.com/keptn-contrib/dynatrace-service/pkg/adapter"
	"github.com/keptn-contrib/dynatrace-service/pkg/credentials"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/gorilla/websocket"
	"github.com/keptn-contrib/dynatrace-service/pkg/lib"
	keptn "github.com/keptn/go-utils/pkg/lib"
)

type ConfigureMonitoringEventHandler struct {
	Event            cloudevents.Event
	IsCombinedLogger bool
	WebSocket        *websocket.Conn
	KeptnHandler     *keptnv2.Keptn
	dtConfigGetter   adapter.DynatraceConfigGetterInterface
}

type KeptnAPIConnectionCheck struct {
	APIURL               string
	ConnectionSuccessful bool
	Message              string
}

func (eh ConfigureMonitoringEventHandler) HandleEvent() error {
	var shkeptncontext string
	_ = eh.Event.Context.ExtensionAs("shkeptncontext", &shkeptncontext)

	if eh.Event.Type() == keptn.ConfigureMonitoringEventType {
		eventData := &keptn.ConfigureMonitoringEventData{}
		if err := eh.Event.DataAs(eventData); err != nil {
			return err
		}
		if eventData.Type != "dynatrace" {
			return nil
		}
	}
	err := eh.configureMonitoring()
	if err != nil {
		log.WithError(err).Error("Configure monitoring failed")
	}
	return nil
}

func (eh *ConfigureMonitoringEventHandler) configureMonitoring() error {
	log.Info("Configuring Dynatrace monitoring")
	e := &keptn.ConfigureMonitoringEventData{}
	err := eh.Event.DataAs(e)
	if err != nil {
		return fmt.Errorf("could not parse event payload: %v", err)
	}
	if e.Type != "dynatrace" {
		return nil
	}

	keptnAPICheck := &KeptnAPIConnectionCheck{}
	// check the connection to the Keptn API
	keptnCredentials, err := credentials.GetKeptnCredentials()
	if err != nil {
		log.WithError(err).Error("Failed to get Keptn API credentials")
		keptnAPICheck.Message = "Failed to get Keptn API Credentials"
		keptnAPICheck.ConnectionSuccessful = false
		keptnAPICheck.APIURL = "unknown"
	} else {
		keptnAPICheck.APIURL = keptnCredentials.APIURL
		log.WithField("apiUrl", keptnCredentials.APIURL).Print("Verifying access to Keptn API")

		err = credentials.CheckKeptnConnection(keptnCredentials)
		if err != nil {
			keptnAPICheck.ConnectionSuccessful = false
			keptnAPICheck.Message = "Warning: Keptn API connection cannot be verified. This might be due to a no-loopback policy of your LoadBalancer. The endpoint might still be reachable from outside the cluster."
			log.WithError(err).Warn(keptnAPICheck.Message)
		} else {
			keptnAPICheck.ConnectionSuccessful = true
		}
	}

	keptnHandler, err := keptnv2.NewKeptn(&eh.Event, keptncommon.KeptnOpts{})
	if err != nil {
		return fmt.Errorf("could not create Keptn handler: %v", err)
	}
	eh.KeptnHandler = keptnHandler

	var shipyard *keptnv2.Shipyard
	if e.Project != "" {
		shipyard, err = keptnHandler.GetShipyard()
		if err != nil {
			msg := fmt.Sprintf("failed to retrieve shipyard for project %s: %v", e.Project, err)
			return eh.handleError(e, msg)
		}
	}

	keptnEvent := adapter.NewConfigureMonitoringAdapter(*e, keptnHandler.KeptnContext, eh.Event.Source())

	dynatraceConfig, err := eh.dtConfigGetter.GetDynatraceConfig(keptnEvent)
	if err != nil {
		msg := fmt.Sprintf("failed to load Dynatrace config: %v", err)
		return eh.handleError(e, msg)
	}
	creds, err := credentials.GetDynatraceCredentials(dynatraceConfig)
	if err != nil {
		msg := fmt.Sprintf("failed to load Dynatrace credentials: %v", err)
		return eh.handleError(e, msg)
	}
	dtHelper := lib.NewDynatraceHelper(keptnHandler, creds)

	configuredEntities, err := dtHelper.ConfigureMonitoring(e.Project, shipyard)
	if err != nil {
		return eh.handleError(e, err.Error())
	}

	log.Info("Dynatrace Monitoring setup done")

	if err := eh.sendConfigureMonitoringFinishedEvent(e, keptnv2.StatusSucceeded, keptnv2.ResultPass, getConfigureMonitoringResultMessage(keptnAPICheck, configuredEntities)); err != nil {
		log.WithError(err).Error("Failed to send configure monitoring finished event")
	}
	return nil
}

func getConfigureMonitoringResultMessage(apiCheck *KeptnAPIConnectionCheck, entities *lib.ConfiguredEntities) string {
	if entities == nil {
		return ""
	}
	msg := "Dynatrace monitoring setup done.\nThe following entities have been configured:\n\n"

	if entities.ManagementZonesEnabled && len(entities.ManagementZones) > 0 {
		msg = msg + "---Management Zones:--- \n"
		for _, mz := range entities.ManagementZones {
			if mz.Success {
				msg = msg + "  - " + mz.Name + ": Created successfully \n"
			} else {
				msg = msg + "  - " + mz.Name + ": Error: " + mz.Message + "\n"
			}
		}
		msg = msg + "\n\n"
	}

	if entities.TaggingRulesEnabled && len(entities.TaggingRules) > 0 {
		msg = msg + "---Automatic Tagging Rules:--- \n"
		for _, mz := range entities.TaggingRules {
			if mz.Success {
				msg = msg + "  - " + mz.Name + ": Created successfully \n"
			} else {
				msg = msg + "  - " + mz.Name + ": Error: " + mz.Message + "\n"
			}
		}
		msg = msg + "\n\n"
	}

	if entities.ProblemNotificationsEnabled {
		msg = msg + "---Problem Notification:--- \n"
		msg = msg + "  - " + entities.ProblemNotifications.Message
		msg = msg + "\n\n"
	}

	if entities.MetricEventsEnabled && len(entities.MetricEvents) > 0 {
		msg = msg + "---Metric Events:--- \n"
		for _, mz := range entities.MetricEvents {
			if mz.Success {
				msg = msg + "  - " + mz.Name + ": Created successfully \n"
			} else {
				msg = msg + "  - " + mz.Name + ": Error: " + mz.Message + "\n"
			}
		}
		msg = msg + "\n\n"
	}

	if entities.DashboardEnabled && entities.Dashboard.Message != "" {
		msg = msg + "---Dashboard:--- \n"
		msg = msg + "  - " + entities.Dashboard.Message
		msg = msg + "\n\n"
	}

	if apiCheck != nil {
		msg = msg + "---Keptn API Connection Check:--- \n"
		msg = msg + "  - Keptn API URL: " + apiCheck.APIURL + "\n"
		msg = msg + fmt.Sprintf("  - Connection Successful: %v. %s\n", apiCheck.ConnectionSuccessful, apiCheck.Message)
		msg = msg + "\n"
	}

	return msg
}

func (eh *ConfigureMonitoringEventHandler) handleError(e *keptn.ConfigureMonitoringEventData, msg string) error {
	log.Error(msg)
	if err := eh.sendConfigureMonitoringFinishedEvent(e, keptnv2.StatusErrored, keptnv2.ResultFailed, msg); err != nil {
		log.WithError(err).Error("Failed to send configure monitoring finished event")
	}
	return errors.New(msg)
}

func (eh *ConfigureMonitoringEventHandler) sendConfigureMonitoringFinishedEvent(configureMonitoringData *keptn.ConfigureMonitoringEventData, status keptnv2.StatusType, result keptnv2.ResultType, message string) error {

	cmFinishedEvent := &keptnv2.ConfigureMonitoringFinishedEventData{
		EventData: keptnv2.EventData{
			Project: configureMonitoringData.Project,
			Service: configureMonitoringData.Service,
			Status:  status,
			Result:  result,
			Message: message,
		},
	}

	keptnContext, _ := eh.Event.Context.GetExtension("shkeptncontext")

	event := cloudevents.NewEvent()
	event.SetSource("dynatrace-service")
	event.SetDataContentType(cloudevents.ApplicationJSON)
	event.SetType(keptnv2.GetFinishedEventType(keptnv2.ConfigureMonitoringTaskName))
	event.SetData(cloudevents.ApplicationJSON, cmFinishedEvent)
	event.SetExtension("shkeptncontext", keptnContext)
	event.SetExtension("triggeredid", eh.Event.Context.GetID())

	if err := eh.KeptnHandler.SendCloudEvent(event); err != nil {
		return fmt.Errorf("could not send %s event: %s", keptnv2.GetFinishedEventType(keptnv2.ConfigureMonitoringTaskName), err.Error())
	}

	return nil
}
