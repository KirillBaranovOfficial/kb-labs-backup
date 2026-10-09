import type { DetailedDiscovery } from './discover.js';

let registeredDiscovery: DetailedDiscovery | undefined;

export function setRegisteredDiscovery(discovery: DetailedDiscovery | undefined): void {
  registeredDiscovery = discovery;
}

export function getRegisteredDiscovery(): DetailedDiscovery | undefined {
  return registeredDiscovery;
}
