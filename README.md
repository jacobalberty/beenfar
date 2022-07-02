[![Coverage Status](https://coveralls.io/repos/github/jacobalberty/beenfar/badge.svg)](https://coveralls.io/github/jacobalberty/beenfar)
[![CodeQL](https://github.com/jacobalberty/beenfar/workflows/CodeQL/badge.svg)](https://github.com/jacobalberty/beenfar/actions?query=workflow%3ACodeQL)

# Has Anyone Really Been Far Even as Decided to Use Even Go Want to do Look More Like?

## What is this?

This is a work in progress network controller to support centralized provisioning and configuration of different types of network gear.

This is still very early on and so far only contains the backend code.

## Todo
* Network configuration endpoints
* Authentication, Authorization and Accounting
* Per device command queues
* Translate internal configuration data to per device data
* Persistent data storage
* Frontend

## Supported device types

### In process
* UniFi access points
* UniFi network switches

### Planned
* OpenWRT
* EdgeOS devices
* Mobile device provisioning
* UniFi gateways

## Data storage
The database layer will be a special device type that accepts all data types and automatically provides its data to the data layer on startup.

Data will be persisted to the dastabase as part of the standard provisioning process, the data storage will just be another device that gets provisioned when data changes.