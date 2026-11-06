import { FloatingSelect } from 'homelab-ui';

const options = [
  { value: 'eu', label: 'Europe (Paris)' },
  { value: 'us', label: 'US East (Virginia)' },
];
const noop = () => {};
export const Default = () => <FloatingSelect className="w-80" label="Region" value="eu" onValueChange={noop} options={options} />;
export const WithHelp = () => (
  <FloatingSelect className="w-80" label="Region" value="us" onValueChange={noop} options={options} description="Backups stay in this region." />
);
export const Loading = () => <FloatingSelect className="w-80" label="Region" value="eu" onValueChange={noop} options={options} loading />;
export const Disabled = () => <FloatingSelect className="w-80" label="Region" value="eu" onValueChange={noop} options={options} disabled />;
