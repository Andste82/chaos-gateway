// chaosctl is a Go program: the CA file that Node gets as NODE_EXTRA_CA_CERTS reaches it as CHAOSGW_CA_CERT, or the tests
// that run it fail with "x509: certificate signed by unknown authority" against a gateway with a self-signed certificate
// while the tests of the generated client pass.
import { chaosctl } from './helpers';

const names = ['CHAOSCTL', 'NODE_EXTRA_CA_CERTS', 'CHAOSGW_CA_CERT', 'CHAOSGW_FINGERPRINT', 'CHAOSGW_INSECURE'];

describe('the environment of chaosctl', () => {
  const saved: Record<string, string | undefined> = {};
  beforeEach(() => {
    for (const n of names) saved[n] = process.env[n];
    for (const n of names) delete process.env[n];
    process.env.CHAOSCTL = 'env'; // prints its environment instead of running chaosctl
  });
  afterEach(() => {
    for (const n of names) {
      if (saved[n] === undefined) delete process.env[n];
      else process.env[n] = saved[n];
    }
  });

  const seen = () => Object.fromEntries(chaosctl().stdout.split('\n').filter((l) => l.includes('=')).map((l) => [l.slice(0, l.indexOf('=')), l.slice(l.indexOf('=') + 1)]));

  test('NODE_EXTRA_CA_CERTS is handed on as CHAOSGW_CA_CERT', () => {
    process.env.NODE_EXTRA_CA_CERTS = '/tmp/gateway.pem';
    expect(seen().CHAOSGW_CA_CERT).toBe('/tmp/gateway.pem');
  });

  test('a way of its own is left alone', () => {
    process.env.NODE_EXTRA_CA_CERTS = '/tmp/gateway.pem';
    process.env.CHAOSGW_FINGERPRINT = 'ab'.repeat(32);
    expect(seen().CHAOSGW_CA_CERT).toBeUndefined();
    delete process.env.CHAOSGW_FINGERPRINT;
    process.env.CHAOSGW_CA_CERT = '/tmp/other.pem';
    expect(seen().CHAOSGW_CA_CERT).toBe('/tmp/other.pem');
  });

  test('nothing is invented when Node trusts no extra certificate', () => {
    expect(seen().CHAOSGW_CA_CERT).toBeUndefined();
  });
});
