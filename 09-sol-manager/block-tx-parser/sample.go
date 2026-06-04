package main

const sampleBlockJSON = `{
  "blockHeight": 300000001,
  "blockTime": 1780539609,
  "blockhash": "7PtnQ1VxK6x9gHfK9x2q4GmqL7jN9tN1Yz1K4m7QYq3",
  "parentSlot": 299999999,
  "previousBlockhash": "4vJ9JU1bJJE96FwsQ4Dq6T6zkxx7YxK7XhRZpYxPzQGp",
  "transactions": [
    {
      "transaction": {
        "signatures": ["5j7s4VqZ6V6Q9b3Y5mDk8oD8m1qJkT7uQ9R9r5wV1zQ5f4d3s2a1p9m8n7b6v5c4x3z2a1s9d8f7g6h5j4k3"],
        "message": {
          "accountKeys": [
            {"pubkey":"3b6n97z5VgqqU4G4YfP7u5QmG2h76nHn8R7VwJ5fW6xk","signer":true,"writable":true},
            {"pubkey":"Fj8tV9p5r3jYgS8xBQk9u1h6z7nQ7p9v3S6xXQ5h9k1B","signer":false,"writable":true},
            {"pubkey":"11111111111111111111111111111111","signer":false,"writable":false}
          ],
          "recentBlockhash": "7PtnQ1VxK6x9gHfK9x2q4GmqL7jN9tN1Yz1K4m7QYq3",
          "instructions": [
            {
              "programId":"11111111111111111111111111111111",
              "program":"system",
              "parsed":{
                "type":"transfer",
                "info":{
                  "source":"3b6n97z5VgqqU4G4YfP7u5QmG2h76nHn8R7VwJ5fW6xk",
                  "destination":"Fj8tV9p5r3jYgS8xBQk9u1h6z7nQ7p9v3S6xXQ5h9k1B",
                  "lamports":1000000000
                }
              }
            }
          ]
        }
      },
      "meta": {
        "err": null,
        "fee": 5000,
        "preBalances": [5000000000, 1000000, 1],
        "postBalances": [3999995000, 1001000000, 1],
        "preTokenBalances": [],
        "postTokenBalances": []
      }
    }
  ]
}`
