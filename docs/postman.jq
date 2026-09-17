# Applied to the converted collection by `make postman`. The converter sends
# {{bearerToken}} on every request that needs a credential but doesn't
# declare it; this declares it, and has Sign in set it.
#
# The converter also fills every query parameter with a value, often a
# placeholder such as <dateTime> that the API refuses, or a random tenant.
# Optional ones start unticked instead, to be ticked and filled in as needed.
.variable += [
  {
    key: "email",
    value: "admin@loghub.local",
    description: "Who Sign in signs in as. An admin may ingest and search; a viewer, such as viewer@demoa.local, may only search."
  },
  {
    key: "password",
    value: "",
    description: "The password for email: ADMIN_PASSWORD or VIEWER_PASSWORD in .env."
  },
  {
    key: "bearerToken",
    value: "",
    description: "Sent by every request that needs a credential. Sign in sets it. For the ingest requests, the ingest key (INGEST_TOKEN in .env) works too."
  }
]
| (.item[] | select(.name == "Auth") | .item[] | select(.name == "Sign in")) |= (
    .request.body.raw = "{\n  \"email\": \"{{email}}\",\n  \"password\": \"{{password}}\"\n}"
    | .event = [{
        listen: "test",
        script: {
          type: "text/javascript",
          exec: [
            "// The requests that need a credential send {{bearerToken}}.",
            "if (pm.response.code === 200) {",
            "  pm.collectionVariables.set(\"bearerToken\", pm.response.json().token);",
            "}"
          ]
        }
      }]
  )
| (.. | objects | select(.query? | type == "array") | .query[]
    | select(.description.content? // "" | startswith("(Required)") | not)
  ).disabled = true
