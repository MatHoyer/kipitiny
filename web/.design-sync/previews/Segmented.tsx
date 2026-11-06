import { Segmented, SegmentedItem } from 'homelab-ui';
import { LayoutGrid, Table2 } from 'lucide-react';

const noop = () => {};
export const WithIcons = () => (
  <Segmented value="cards" onValueChange={noop} aria-label="View">
    <SegmentedItem value="cards"><LayoutGrid />Projects</SegmentedItem>
    <SegmentedItem value="table"><Table2 />Services</SegmentedItem>
  </Segmented>
);
export const Small = () => (
  <Segmented size="sm" value="7d" onValueChange={noop} aria-label="Range">
    <SegmentedItem value="24h">24h</SegmentedItem>
    <SegmentedItem value="7d">7 days</SegmentedItem>
    <SegmentedItem value="30d">30 days</SegmentedItem>
  </Segmented>
);
