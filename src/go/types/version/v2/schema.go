package v2

var OpenAPI = []byte(`
openapi: "3.0.0"
info:
  title: phenix config specs
  version: "2.0"
paths: {}
components:
  schemas:
    Image:
      type: object
      required:
      - format
      - mirror
      - release
      - size
      - variant
      properties:
        compress:
          type: boolean
          default: false
          example: false
        deb_append:
          type: string
          example: --components=main,restricted
        format:
          type: string
          example: qcow2
        mirror:
          type: string
          example: http://us.archive.ubuntu.com/ubuntu/
        overlays:
          type: array
          items:
            type: string
          example:
          - /phenix/vmdb/overlays/example-overlay
        packages:
          type: array
          items:
            type: string
          example:
          - isc-dhcp-client
          - openssh-server
        ramdisk:
          type: boolean
          default: false
          example: false
        release:
          type: string
          example: focal
        script_order:
          type: array
          items:
            type: string
          example:
          - POSTBUILD_APT_CLEANUP
        scripts:
          type: object
          nullable: true
          additionalProperties:
            type: string
          example:
            POSTBUILD_APT_CLEANUP: |
              apt clean || apt-get clean || echo "unable to clean apt cache"
        size:
          type: string
          example: 10G
        variant:
          type: string
          example: minbase
    Role:
      type: object
      required:
      - policies
      - roleName
      properties:
        policies:
          type: array
          items:
            type: object
            properties:
              resources:
                type: array
                items:
                  type: string
              resourceNames:
                type: array
                items:
                  type: string
              verbs:
                type: array
                items:
                  type: string
          example:
          - resources:
            - experiments
            - experiments/*
            resourceNames:
            - '*'
            verbs:
            - list
            - get
        roleName:
          type: string
          example: Example Role
    User:
      type: object
      required:
      - first_name
      - last_name
      - username
      properties:
        first_name:
          type: string
          example: John
        last_name:
          type: string
          example: Doe
        password:
          type: string
          example: '<encrypted password>'
          readOnly: true
        rbac:
          allOf:
          - $ref: "#/components/schemas/Role"
          readOnly: true
        username:
          type: string
          example: johndoe@example.com
    Topology:
      type: object
      required:
      - nodes
      properties:
        nodes:
          type: array
          items:
            $ref: "#/components/schemas/Node"
    Scenario:
      type: object
      required:
      - apps
      properties:
        apps:
          type: array
          items:
            type: object
            required:
            - name
            properties:
              name:
                type: string
                example: example-app
              assetDir:
                type: string
                example: /phenix/topologies/example-topo/assets
              metadata:
                type: object
                nullable: true
                additionalProperties: true
                example:
                  setting0: true
                  setting1: 42
                  setting2: universe key
              hosts:
                type: array
                items:
                  type: object
                  required:
                  - hostname
                  properties:
                    hostname:
                      type: string
                      example: example-host
                    metadata:
                      type: object
                      nullable: true
                      additionalProperties: true
                      example:
                        setting0: true
                        setting1: 42
                        setting2: universe key
    Experiment:
      type: object
      required:
      - topology
      properties:
        topology:
          $ref: "#/components/schemas/Topology"
        scenario:
          nullable: true
          allOf:
          - $ref: "#/components/schemas/Scenario"
        baseDir:
          type: string
          example: /phenix/topologies/example-topo
        experimentName:
          type: string
          example: example-exp
          readOnly: true
        vlans:
          type: object
          nullable: true
          properties:
            aliases:
              type: object
              nullable: true
              additionalProperties:
                type: integer
              example:
                MGMT: 200
            min:
              type: integer
            max:
              type: integer
        schedule:
          type: object
          nullable: true
          additionalProperties:
            type: string
          example:
            ADServer: compute1
    Node:
      type: object
      required:
      - type
      - general
      - hardware
      properties:
        type:
          type: string
          enum:
          - Firewall
          - Printer
          - Router
          - Server
          - Switch
          - VirtualMachine
          default: VirtualMachine
          example: VirtualMachine
        general:
          type: object
          required:
          - hostname
          properties:
            hostname:
              type: string
              example: ADServer
            description:
              type: string
              example: Active Directory Server
            vm_type:
              type: string
              enum:
              - kvm
              - container
              - ""
              default: kvm
              example: kvm
            snapshot:
              type: boolean
              default: false
              example: false
              nullable: true
            do_not_boot:
              type: boolean
              default: false
              example: false
              nullable: true
        hardware:
          type: object
          required:
          - os_type
          - drives
          properties:
            cpu:
              type: string
              enum:
              - Broadwell
              - Haswell
              - core2duo
              - pentium3
              - host
              - ""
              default: Broadwell
              example: Broadwell
            vcpus:
              type: integer
              default: 1
              example: 4
            memory:
              type: integer
              default: 1024
              example: 8192
            os_type:
              type: string
              enum:
              - windows
              - linux
              - rhel
              - centos
              - vyatta
              - minirouter
              default: linux
              example: windows
            drives:
              type: array
              items:
                type: object
                required:
                - image
                properties:
                  image:
                    type: string
                    example: ubuntu.qc2
                  interface:
                    type: string
                    enum:
                    - ahci
                    - ide
                    - scsi
                    - sd
                    - mtd
                    - floppy
                    - pflash
                    - virtio
                    - ""
                    default: ide
                    example: ide
                  cache_mode:
                    type: string
                    enum:
                    - none
                    - writeback
                    - unsafe
                    - directsync
                    - writethrough
                    - ""
                    default: writeback
                    example: writeback
                  inject_partition:
                    type: integer
                    default: 1
                    example: 2
                    nullable: true
        network:
          type: object
          nullable: true
          required:
          - interfaces
          properties:
            interfaces:
              type: array
              items:
                type: object
                oneOf:
                - $ref: '#/components/schemas/static_iface'
                - $ref: '#/components/schemas/dhcp_iface'
                - $ref: '#/components/schemas/serial_iface'
            routes:
              type: array
              items:
                type: object
                required:
                - destination
                - next
                - cost
                properties:
                  destination:
                    type: string
                    example: 192.168.0.0/24
                  next:
                    type: string
                    example: 192.168.1.254
                  cost:
                    type: integer
                    default: 1
                    example: 1
            ospf:
              type: object
              nullable: true
              required:
              - router_id
              - areas
              properties:
                router_id:
                  type: string
                  example: 0.0.0.1
                areas:
                  type: array
                  items:
                    type: object
                    required:
                    - area_id
                    - area_networks
                    properties:
                      area_id:
                        type: integer
                        example: 1
                        default: 1
                      area_networks:
                        type: array
                        items:
                          type: object
                          required:
                          - network
                          properties:
                            network: 
                              type: string
                              example: 10.1.25.0/24
            rulesets:
              type: array
              items:
                type: object
                required:
                - name
                - default
                - rules
                properties:
                  name:
                    type: string
                    example: OutToDMZ
                  description:
                    type: string
                    example: From Corp to the DMZ network
                  default:
                    type: string
                    enum:
                    - accept
                    - drop
                    - reject
                    example: drop
                  rules:
                    type: array
                    items:
                      type: object
                      required:
                      - id
                      - action
                      - protocol
                      properties:
                        id:
                          type: integer
                          example: 10
                        description:
                          type: string
                          example: Allow UDP 10.1.26.80 ==> 10.2.25.0/24:123
                        action:
                          type: string
                          enum:
                          - accept
                          - drop
                          - reject
                          example: accept
                        protocol:
                          type: string
                          enum:
                          - tcp
                          - udp
                          - icmp
                          - esp
                          - ah
                          - all
                          default: tcp
                          example: tcp
                        source:
                          type: object
                          nullable: true
                          required:
                          - address
                          properties:
                            address:
                              type: string
                              example: 10.1.24.60
                            port:
                              type: integer
                              example: 3389
                        destination:
                          type: object
                          nullable: true
                          required:
                          - address
                          properties:
                            address:
                              type: string
                              example: 10.1.24.60
                            port:
                              type: integer
                              example: 3389
        injections:
          type: array
          items:
            type: object
            required:
            - src
            - dst
            properties:
              src:
                type: string
                example: foo.xml
              dst:
                type: string
                example: /etc/phenix/foo.xml
              description:
                type: string
                example: phenix config file
              permissions:
                type: string
                example: '0664'
        advanced:
          type: object
          nullable: true
          additionalProperties:
            type: string
    iface:
      type: object
      required:
      - name
      - vlan
      properties:
        name:
          type: string
          example: eth0
        vlan:
          type: string
          example: EXP-1
        autostart:
          type: boolean
          default: true
        mac:
          type: string
          example: 00:11:22:33:44:55
        mtu:
          type: integer
          default: 1500
          example: 1500
        bridge:
          type: string
          default: phenix
    iface_address:
      type: object
      required:
      - address
      - mask
      properties:
        address:
          type: string
          format: ipv4
          example: 192.168.1.100
        mask:
          type: integer
          minimum: 0
          maximum: 32
          default: 24
          example: 24
        gateway:
          type: string
          format: ipv4
          example: 192.168.1.1
    iface_rulesets:
      type: object
      properties:
        ruleset_out:
          type: string
          example: OutToInet
        ruleset_in:
          type: string
          example: InFromInet
    static_iface:
      allOf:
      - $ref: '#/components/schemas/iface'
      - $ref: '#/components/schemas/iface_address'
      - $ref: '#/components/schemas/iface_rulesets'
      required:
      - type
      - proto
      properties:
        type:
          type: string
          enum:
          - ethernet
          default: ethernet
          example: ethernet
        proto:
          type: string
          enum:
          - static
          - ospf
          default: static
          example: static
    dhcp_iface:
      allOf:
      - $ref: '#/components/schemas/iface'
      - $ref: '#/components/schemas/iface_rulesets'
      required:
      - type
      - proto
      properties:
        type:
          type: string
          enum:
          - ethernet
          default: ethernet
          example: ethernet
        proto:
          type: string
          enum:
          - dhcp
          default: dhcp
          example: dhcp
    serial_iface:
      allOf:
      - $ref: '#/components/schemas/iface'
      - $ref: '#/components/schemas/iface_address'
      - $ref: '#/components/schemas/iface_rulesets'
      required:
      - type
      - proto
      - udp_port
      - baud_rate
      - device
      properties:
        type:
          type: string
          enum:
          - serial
          default: serial
          example: serial
        proto:
          type: string
          enum:
          - static
          default: static
          example: static
        udp_port:
          type: integer
          minimum: 0
          maximum: 65535
          default: 8989
          example: 8989
        baud_rate:
          type: integer
          enum:
          - 110
          - 300
          - 600
          - 1200
          - 2400
          - 4800
          - 9600
          - 14400
          - 19200
          - 38400
          - 57600
          - 115200
          - 128000
          - 256000
          default: 9600
          example: 9600
        device:
          type: string
          default: /dev/ttyS0
          example: /dev/ttyS0
`)
