package v0

var OpenAPI = []byte( //nolint:gochecknoglobals // global constant
	"\nopenapi: \"3.0.0\"\ninfo:\n  title: phenix\n  version: \"1.0\"\npaths: {}\ncomponents:\n  schemas:\n    Topology:\n      type: object\n      title: Demo Topology\n      required:\n      - nodes\n      properties:\n        nodes:\n          type: array\n          title: Nodes\n          items:\n            $ref: \"#/components/schemas/Node\"\n    Node:\n      type: object\n      title: Node\n      required:\n      - type\n      properties:\n        type:\n          string\n          title: Node Type\n          enum:\n          - Firewall\n          - Printer\n          - Router\n          - Server\n          - Switch\n          - VirtualMachine\n          default: VirtualMachine\n          example:",
)
