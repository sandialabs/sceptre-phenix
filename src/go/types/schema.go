package types

//nolint:gochecknoglobals // global constant
var OpenAPI = []byte(`
openapi: "3.0.0"
info:
  title: phenix
  version: "2.0"
paths: {}
components:
  schemas:
    Config:
      type: object
      required:
      - apiVersion
      - kind
      - metadata
      - spec
      properties:
        apiVersion:
          type: string
          pattern: '^phenix\.sandia\.gov\/v\d+.*$'
        kind:
          type: string
          enum:
          - User
          - Role
          - Image
          - Topology
          - Scenario
          - Experiment
        metadata:
          type: object
          required:
          - name
          properties:
            name:
              type: string
              minLength: 1
              pattern: '^[a-zA-Z0-9_@.-]*$'
            created:
              type: string
            updated:
              type: string
            annotations:
              type: object
              properties:
                builder-doc:
                  type: object
                  minProperties: 1
                  additionalProperties: false
                  properties:
                    digest:
                      type: string
                    id:
                      type: string
                    path:
                      type: string
              additionalProperties:
                type: string
        spec:
          type: object
`)
