import {
  convertCertificateToCertificatePEM,
  convertKeyPairToPEM,
  generateKeyPair,
  generateSelfSignedCodeSigningCertificate,
} from '@expo/code-signing-certificates';
import { Command } from '@oclif/core';
import { ensureDirSync, writeFile } from 'fs-extra';
import path from 'path';

import Log from '../lib/log';
import { promptAsync } from '../lib/prompts';

export default class GenerateCerts extends Command {
  static override args = {};
  static override description = 'Generate private & public certificates for code signing';
  static override examples = ['<%= config.bin %> <%= command.id %>'];
  static override flags = {};
  public async run(): Promise<void> {
    const { certificateOutputDir } = await promptAsync({
      message:
        'In which directory would you like to store your code signing certificate (used by your expo app)?',
      name: 'certificateOutputDir',
      type: 'text',
      initial: './certs',
      validate: v => {
        try {
          // eslint-disable-next-line
          ensureDirSync(path.join(process.cwd(), v));
          return true;
        } catch {
          return false;
        }
      },
    });
    const { keyOutputDir } = await promptAsync({
      message:
        'In which directory would you like to store your key pair (used by your OTA Server) ?. ⚠️ Those certss are sensitive and should be kept private.',
      name: 'keyOutputDir',
      type: 'text',
      initial: './certs',
      validate: v => {
        try {
          // eslint-disable-next-line
          ensureDirSync(path.join(process.cwd(), v));
          return true;
        } catch {
          return false;
        }
      },
    });
    const { certificateCommonName } = await promptAsync({
      message: 'Please enter your Organization name',
      name: 'certificateCommonName',
      type: 'text',
      initial: 'Your Organization Name',
      validate: v => {
        return !!v;
      },
    });
    const { certificateValidityDurationYears } = await promptAsync({
      message: 'How many years should the certificate be valid for?',
      name: 'certificateValidityDurationYears',
      type: 'number',
      initial: 10,
      validate: v => {
        return v > 0 && Number.isInteger(v);
      },
    });
    const validityDurationYears = Math.floor(Number(certificateValidityDurationYears));
    const certificateOutput = path.resolve(process.cwd(), certificateOutputDir);
    const keyOutput = path.resolve(process.cwd(), keyOutputDir);
    const validityNotBefore = new Date();
    const validityNotAfter = new Date();
    validityNotAfter.setFullYear(validityNotAfter.getFullYear() + validityDurationYears);
    const keyPair = generateKeyPair();
    const certificate = generateSelfSignedCodeSigningCertificate({
      keyPair,
      validityNotBefore,
      validityNotAfter,
      commonName: certificateCommonName,
    });
    const keyPairPEM = convertKeyPairToPEM(keyPair);
    const certificatePEM = convertCertificateToCertificatePEM(certificate);
    await Promise.all([
      writeFile(path.join(keyOutput, 'public-key.pem'), keyPairPEM.publicKeyPEM),
      writeFile(path.join(keyOutput, 'private-key.pem'), keyPairPEM.privateKeyPEM),
      writeFile(path.join(certificateOutput, 'certificate.pem'), certificatePEM),
    ]);
    Log.succeed(
      `Generated public and private keys output in ${keyOutputDir}. Please follow the documentation to securely store them and do not commit them to your repository.`
    );
    Log.succeed(`Generated code signing certificate output in ${certificateOutputDir}.`);
  }
}
