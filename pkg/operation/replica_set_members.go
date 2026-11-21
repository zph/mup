package operation

import (
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
)

// Per-member settings carried in an init_replica_set operation's
// "member_options" parameter, keyed by member host:port.
const (
	paramMemberOptions = "member_options"
	memberOptPriority  = "priority"
	memberOptHidden    = "hidden"
	memberOptVotes     = "votes"
)

// replicaSetMemberDocs builds replSetInitiate member documents. options maps a
// member's host:port to the priority, hidden and votes settings from the
// topology; members without an entry get MongoDB's defaults.
// LOCAL-022
func replicaSetMemberDocs(members []interface{}, options interface{}) ([]bson.M, error) {
	if len(members) == 0 {
		return nil, fmt.Errorf("no members provided")
	}

	memberOptions, err := memberOptionsParam(options)
	if err != nil {
		return nil, err
	}

	docs := make([]bson.M, len(members))
	seen := make(map[string]bool, len(members))
	for i, m := range members {
		hostPort, ok := m.(string)
		if !ok {
			return nil, fmt.Errorf("member %d is not a string", i)
		}
		seen[hostPort] = true

		doc := bson.M{"_id": i, "host": hostPort}
		opts := memberOptions[hostPort]
		if priority, ok := numberOption(opts[memberOptPriority]); ok {
			doc[memberOptPriority] = priority
		}
		if hidden, ok := opts[memberOptHidden].(bool); ok {
			doc[memberOptHidden] = hidden
		}
		if votes, ok := numberOption(opts[memberOptVotes]); ok {
			doc[memberOptVotes] = int(votes)
		}
		docs[i] = doc
	}

	for hostPort := range memberOptions {
		if !seen[hostPort] {
			return nil, fmt.Errorf("member options given for %s, which is not a member", hostPort)
		}
	}
	return docs, nil
}

// memberOptionsParam accepts the in-memory form and the form produced by
// loading a plan from JSON.
func memberOptionsParam(raw interface{}) (map[string]map[string]interface{}, error) {
	switch options := raw.(type) {
	case nil:
		return nil, nil
	case map[string]map[string]interface{}:
		return options, nil
	case map[string]interface{}:
		result := make(map[string]map[string]interface{}, len(options))
		for hostPort, value := range options {
			opts, ok := value.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("member options for %s have unexpected type %T", hostPort, value)
			}
			result[hostPort] = opts
		}
		return result, nil
	}
	return nil, fmt.Errorf("%s has unexpected type %T", paramMemberOptions, raw)
}

// numberOption reads a number that is a float64 or int in memory and a
// float64 after a JSON round trip.
func numberOption(raw interface{}) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	}
	return 0, false
}
