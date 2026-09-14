package remote

import "google.golang.org/protobuf/reflect/protoreflect"

var definitions = map[protoreflect.FullName]Definition{
	"inframill.v1.ListBlueprintsResponse":         {Rows: "blueprints", Empty: "No blueprints found.", Fields: []Field{{referencePath, referencePath, "Reference", Text}, {descriptionPath, descriptionPath, descriptionLabel, Text}}},
	"inframill.v1.ListCommandDefinitionsResponse": {Rows: "commands", Empty: "No commands found.", Fields: []Field{{namePath, namePath, nameLabel, Text}, {safetyPath, safetyPath, "Safety", Enum}, {descriptionPath, descriptionPath, descriptionLabel, Text}}},
	"inframill.v1.ListInstancesResponse":          {Rows: "instances", Empty: "No instances found.", NextCursor: "next_cursor", ObservedAt: observedAtPath, Fields: instanceFields("")},
	"inframill.v1.ListCommandExecutionsResponse":  {Rows: "executions", Empty: "No command executions found.", NextCursor: "next_cursor", ObservedAt: observedAtPath, Fields: commandFields("")},
	"inframill.v1.GetInstanceResponse":            {Fields: instanceFields("instance."), Failure: "instance.failure", ObservedAt: observedAtPath, Sections: []Section{{"instance.values", "Values"}}},
	"inframill.v1.InitInstanceResponse":           {Fields: instanceFields("instance."), Failure: "instance.failure", Sections: []Section{{"instance.values", "Values"}}},
	"inframill.v1.FetchBlueprintResponse":         {Fields: blueprintFields("blueprint.")},
	"inframill.v1.BlueprintInfo":                  {Fields: blueprintFields("")},
	"inframill.v1.CommandDefinition":              {Fields: []Field{{namePath, namePath, nameLabel, Text}, {descriptionPath, descriptionPath, descriptionLabel, Text}, {safetyPath, safetyPath, "Safety", Enum}, {"effects", "effects", "Effects", Lines}, {"verification", "verification", "Verification", Text}}, Sections: []Section{{"arguments_schema", "Arguments schema"}}},
	"inframill.v1.SubmitCommandResponse":          {Fields: []Field{{"command_id", "command_id", commandUUIDLabel, Text}}},
	"inframill.v1.GetCommandExecutionResponse":    {Fields: commandDetails(), Failure: "execution.error", ObservedAt: observedAtPath, Sections: []Section{{"execution.request.args", "Args"}, {"execution.result", "Result"}}},
}

func instanceFields(prefix string) []Field {
	return []Field{{namePath, prefix + namePath, nameLabel, Text}, {"id", prefix + "id", "Instance UUID", Text}, {statusPath, prefix + statusPath, "Status", Enum}, {"blueprint", prefix + "blueprint", "Blueprint", Text}}
}

func commandFields(prefix string) []Field {
	return []Field{{"id", prefix + "request.id", commandUUIDLabel, Text}, {"command", prefix + "request.command", "Command", Text}, {statusPath, prefix + statusPath, "Status", Enum}, {"created_at", prefix + "created_at", "Created at", Timestamp}, {"started_at", prefix + "started_at", "Started at", Timestamp}, {"ended_at", prefix + "ended_at", "Ended at", Timestamp}}
}

func commandDetails() []Field {
	fields := commandFields("execution.")
	return append(append(append([]Field{}, fields[:3]...), Field{"idempotency_key", "execution.request.idempotency_key", "Idempotency key", Text}), fields[3:]...)
}

func blueprintFields(prefix string) []Field {
	return []Field{{referencePath, prefix + referencePath, "Reference", Text}, {"repository", prefix + "repository", "Repository", Text}, {"digest", prefix + "digest", "Digest", Text}, {"tags", prefix + "tags", "Tags", Commas}, {"api_version", prefix + "api_version", "API version", Text}, {descriptionPath, prefix + descriptionPath, descriptionLabel, Text}, {"directory", prefix + "directory", "Directory", Text}}
}

const (
	descriptionLabel = "Description"
	nameLabel        = "Name"
	observedAtPath   = "observed_at"
	commandUUIDLabel = "Command UUID"
)

const (
	descriptionPath = "description"
	namePath        = "name"
)

const (
	referencePath = "reference"
	safetyPath    = "safety"
)

const statusPath = "status"
