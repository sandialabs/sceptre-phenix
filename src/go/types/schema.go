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
      title: Config
      description: >-
        A phenix configuration resource with an API version, kind, metadata,
        and kind-specific specification.
      required:
      - apiVersion
      - kind
      - metadata
      - spec
      properties:
        apiVersion:
          type: string
          title: API Version
          description: Version identifier for the phenix configuration API.
          pattern: '^phenix\.sandia\.gov\/v\d+.*$'
          example: phenix.sandia.gov/v1
        kind:
          type: string
          title: Kind
          description: Configuration resource kind.
          enum:
          - User
          - Role
          - Image
          - Topology
          - Scenario
          - Experiment
          example: Topology
        metadata:
          type: object
          title: Metadata
          description: Name and optional metadata for the configuration resource.
          required:
          - name
          properties:
            name:
              type: string
              title: Name
              description: Unique name of the configuration resource.
              minLength: 1
              pattern: '^[a-zA-Z0-9_@.-]*$'
              example: example-topology
            created:
              type: string
              title: Created
              description: RFC 3339 time when the configuration resource was created.
              readOnly: true
              example: "2026-01-01T00:00:00Z"
            updated:
              type: string
              title: Updated
              description: RFC 3339 time when the configuration resource was last updated.
              readOnly: true
              example: "2026-01-02T00:00:00Z"
            annotations:
              type: object
              title: Annotations
              description: String-valued metadata associated with the configuration resource.
              additionalProperties:
                type: string
              example:
                owner: range-team
        spec:
          type: object
          title: Specification
          description: Kind-specific configuration fields.
          example: {}
`)
