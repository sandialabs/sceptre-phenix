// Topologies, and the scenarios written for them.
import axiosInstance from '@/utils/axios.js';

// GET /topologies
export function listTopologies() {
  return axiosInstance.get('topologies');
}

// GET /topologies/{name}/scenarios
export function listTopologyScenarios(topology) {
  return axiosInstance.get(
    `topologies/${encodeURIComponent(topology)}/scenarios`,
  );
}
