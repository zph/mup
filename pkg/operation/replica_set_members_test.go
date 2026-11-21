package operation

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
)

const (
	memberA = "127.0.0.1:30000"
	memberB = "127.0.0.1:30001"
)

// LOCAL-022
func TestReplicaSetMemberDocs(t *testing.T) {
	members := []interface{}{memberA, memberB}
	options := map[string]map[string]interface{}{
		memberB: {memberOptPriority: 0.0, memberOptHidden: true, memberOptVotes: 0},
	}
	want := []bson.M{
		{"_id": 0, "host": memberA},
		{"_id": 1, "host": memberB, "priority": 0.0, "hidden": true, "votes": 0},
	}

	t.Run("in-memory plan", func(t *testing.T) {
		docs, err := replicaSetMemberDocs(members, options)

		require.NoError(t, err)
		assert.Equal(t, want, docs)
	})

	t.Run("json-loaded plan", func(t *testing.T) {
		raw, err := json.Marshal(options)
		require.NoError(t, err)
		var loaded interface{}
		require.NoError(t, json.Unmarshal(raw, &loaded))

		docs, err := replicaSetMemberDocs(members, loaded)

		require.NoError(t, err)
		assert.Equal(t, want, docs)
	})

	t.Run("no options", func(t *testing.T) {
		docs, err := replicaSetMemberDocs(members, nil)

		require.NoError(t, err)
		assert.Equal(t, []bson.M{{"_id": 0, "host": memberA}, {"_id": 1, "host": memberB}}, docs)
	})

	t.Run("rejects options for an unknown member", func(t *testing.T) {
		_, err := replicaSetMemberDocs(members, map[string]map[string]interface{}{"127.0.0.1:9": {memberOptVotes: 0}})

		require.Error(t, err)
	})

	t.Run("rejects non-string members and empty lists", func(t *testing.T) {
		_, err := replicaSetMemberDocs([]interface{}{42}, nil)
		require.Error(t, err)

		_, err = replicaSetMemberDocs(nil, nil)
		require.Error(t, err)
	})
}
