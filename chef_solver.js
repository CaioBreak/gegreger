const crypto = require('crypto');
const https = require('https');

/**
 * ROBLOX CHEF CHALLENGE SOLVER
 * Complete headless automation for the CHEF challenge flow
 */

class ChefChallengeSolver {
  constructor(cookies) {
    this.cookies = cookies; // .ROBLOSECURITY cookie
    this.challengeId = null;
    this.userId = null;
    this.nonce = null;
    this.browserTrackerId = null;
    this.scriptIdentifiers = [];
    this.ecdsaKeyPair = null;
    this.publicKeySerialized = null;
    this.csrfToken = null;
    this.redemptionToken = null; // Token from continue response
  }

  /**
   * Make HTTPS request
   */
  async request(method, url, body = null, headers = {}) {
    return new Promise((resolve, reject) => {
      const urlObj = new URL(url);
      
      const options = {
        hostname: urlObj.hostname,
        path: urlObj.pathname + urlObj.search,
        method: method,
        headers: {
          'User-Agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36',
          'Cookie': this.cookies,
          'Content-Type': 'application/json',
          'Accept': 'application/json',
          ...headers
        }
      };
      
      // Add CSRF token if available and targeting auth.roblox.com
      if (this.csrfToken && urlObj.hostname.includes('auth.roblox.com')) {
        options.headers['X-CSRF-TOKEN'] = this.csrfToken;
      }

      if (body) {
        const bodyStr = JSON.stringify(body);
        options.headers['Content-Length'] = Buffer.byteLength(bodyStr);
      }

      const req = https.request(options, (res) => {
        let data = '';
        res.on('data', (chunk) => data += chunk);
        res.on('end', () => {
          try {
            const jsonData = data ? JSON.parse(data) : {};
            resolve({
              status: res.statusCode,
              headers: res.headers,
              body: jsonData
            });
          } catch (e) {
            resolve({
              status: res.statusCode,
              headers: res.headers,
              body: data
            });
          }
        });
      });

      req.on('error', reject);
      
      if (body) {
        req.write(JSON.stringify(body));
      }
      
      req.end();
    });
  }

  /**
   * Step 0: Get CSRF token
   */
  async getCsrfToken() {
    console.log('[0/9] Fetching CSRF token...');
    
    // Make a request that will fail with 403 but return CSRF token
    const response = await this.request(
      'POST',
      'https://auth.roblox.com/v1/username',
      { username: 'probe' }
    );
    
    // Extract CSRF token from response header
    if (response.headers['x-csrf-token']) {
      this.csrfToken = response.headers['x-csrf-token'];
      console.log(`  CSRF token acquired`);
    } else {
      console.log(`  No CSRF token in response (may not be needed)`);
    }
  }

  /**
   * Step 1: Trigger the challenge
   */
  async triggerChallenge(username) {
    console.log('[1/9] Triggering CHEF challenge...');
    
    const response = await this.request(
      'POST',
      'https://auth.roblox.com/v1/username',
      { username }
    );

    if (response.status !== 401) {
      throw new Error('Challenge not triggered. Status: ' + response.status);
    }

    // Extract challenge metadata from headers
    this.challengeId = response.headers['rblx-challenge-id'];
    const metadataB64 = response.headers['rblx-challenge-metadata'];
    const challengeType = response.headers['rblx-challenge-type'];

    if (challengeType !== 'chef') {
      throw new Error('Not a CHEF challenge. Type: ' + challengeType);
    }

    // Decode metadata
    const metadata = JSON.parse(Buffer.from(metadataB64, 'base64').toString());
    this.userId = metadata.userId;
    this.browserTrackerId = metadata.browserTrackerId;
    this.scriptIdentifiers = metadata.scriptIdentifiers;

    // Extract nonce from contentInlineBase64 or generate
    if (metadata.contentInlineBase64) {
      const decoded = Buffer.from(metadata.contentInlineBase64, 'base64').toString();
      // Try to extract nonce if present in decoded content
      // For now, generate a UUID-like nonce
    }
    this.nonce = this.generateNonce();

    console.log(`  Challenge ID: ${this.challengeId}`);
    console.log(`  User ID: ${this.userId}`);
    console.log(`  Script Identifiers: ${this.scriptIdentifiers.length}`);
    console.log(`  Nonce: ${this.nonce}`);
  }

  /**
   * Step 2: Generate ECDSA key pair
   */
  async generateKeyPair() {
    console.log('[2/9] Generating ECDSA P-256 key pair...');
    
    const { privateKey, publicKey } = crypto.generateKeyPairSync('ec', {
      namedCurve: 'prime256v1',
      publicKeyEncoding: { type: 'spki', format: 'der' },
      privateKeyEncoding: { type: 'pkcs8', format: 'der' }
    });

    this.ecdsaKeyPair = {
      privateKey: crypto.createPrivateKey({
        key: privateKey,
        format: 'der',
        type: 'pkcs8'
      }),
      publicKey: crypto.createPublicKey({
        key: publicKey,
        format: 'der',
        type: 'spki'
      })
    };

    this.publicKeySerialized = publicKey.toString('base64');
    
    console.log(`  Public key generated (${this.publicKeySerialized.length} bytes)`);
  }

  /**
   * Step 3: Register public key with server
   */
  async registerPublicKey() {
    console.log('[3/9] Registering public key with server...');
    
    const response = await this.request(
      'POST',
      'https://apis.roblox.com/rotating-client-service/v1/registration/register',
      {
        identifier: this.nonce,
        key: this.publicKeySerialized
      }
    );

    if (response.status !== 200) {
      throw new Error('Key registration failed: ' + JSON.stringify(response.body));
    }

    console.log(`  Registered (signature received)`);
    return response.body;
  }

  /**
   * Step 4: Fetch fingerprint scripts
   */
  async fetchScripts() {
    console.log('[4/9] Fetching fingerprint scripts...');
    
    // For headless, we skip script fetching since we'll use static fingerprints
    console.log(`  Skipped (using static fingerprints)`);
  }

  /**
   * Step 5: Generate fingerprint data
   */
  generateFingerprint() {
    console.log('[5/9] Generating fingerprint data...');
    
    // Static fingerprint data that mimics Chrome on Windows
    const fingerprint = {
      audio: {
        sampleHash: "124.04347527516074",
        oscillator: "sine",
        maxChannels: 2,
        channelCountMode: "max"
      },
      canvas: {
        commonImageDataHash: "3423728896"
      },
      fonts: {
        "Arial": 567.890625,
        "Helvetica": 567.890625,
        "Times New Roman": 620.4375
      },
      hardware: {
        videocard: {
          vendor: "Google Inc. (NVIDIA)",
          renderer: "ANGLE (NVIDIA, NVIDIA GeForce GTX 1060 6GB Direct3D11 vs_5_0 ps_5_0)",
          version: "WebGL 1.0 (OpenGL ES 2.0 Chromium)",
          shadingLanguageVersion: "WebGL GLSL ES 1.0 (OpenGL ES GLSL ES 1.0 Chromium)"
        },
        architecture: 0,
        deviceMemory: "8",
        jsHeapSizeLimit: 4294705152
      },
      language: {
        languages: ["en-US", "en"],
        timezone: "America/New_York"
      },
      permissions: {},
      plugins: {
        plugins: []
      },
      screen: {
        is_touchscreen: false,
        maxTouchPoints: 0,
        colorDepth: 24,
        mediaMatches: ["pointer: fine", "hover: hover"]
      },
      navigator: {
        platform: "Win32",
        cookieEnabled: true,
        productSub: "20030107",
        product: "Gecko",
        useragent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
        hardwareConcurrency: 8,
        browser: {
          name: "Chrome",
          version: "131"
        },
        applePayVersion: 0
      },
      webgl: {
        commonImageHash: "2891456789"
      },
      math: {
        acos: 1.0471975511965979,
        asin: 0.0,
        atan: 0.0,
        cos: 0.0,
        cosh: 1.3622921248490338,
        e: 2.718281828459045,
        largeCos: -0.9999999999999999,
        largeSin: 0.000000000000004440892098500626,
        largeTan: -0.00000000000000444089209850063,
        log: 6.907755278982137,
        pi: 3.141592653589793,
        sin: 0.0,
        sinh: 0.0,
        sqrt: 1.4142135623730951,
        tan: 0.0,
        tanh: 0.0
      }
    };

    console.log(`  Fingerprint generated`);
    return fingerprint;
  }

  /**
   * Step 6: Sign payload with ECDSA
   */
  signPayload(payload) {
    const payloadStr = JSON.stringify(payload);
    const sign = crypto.createSign('SHA256');
    sign.update(payloadStr);
    sign.end();
    
    const signature = sign.sign(this.ecdsaKeyPair.privateKey);
    return signature.toString('base64');
  }

  /**
   * Step 7: Encrypt payload with RSA + AES
   */
  encryptPayload(payload) {
    // RSA public key (hardcoded in Roblox scripts)
    const rsaPublicKeyPem = `-----BEGIN PUBLIC KEY-----
MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDc1A+KfHQKIg3NZ4eeffpDBQy9
aU3ZERshD6CT2oGT1ghk3iTZzMI1mn8HGY98GUiKmPyJobFyFBOZZqZU6TjajlGF
rskF2zMjpJegI8hCberJ1QlesGTnelxET0Y9RCo0mIKbE0kFpwK/YorzM/1bCg36
vSYLiegVEXcLDcTYpwIDAQAB
-----END PUBLIC KEY-----`;

    // Generate AES key and IV
    const aesKey = crypto.randomBytes(32); // 256 bits
    const iv = crypto.randomBytes(12);     // 96 bits for GCM

    // Encrypt payload with AES-256-GCM
    const cipher = crypto.createCipheriv('aes-256-gcm', aesKey, iv);
    const payloadStr = JSON.stringify(payload);
    let encrypted = cipher.update(payloadStr, 'utf8');
    encrypted = Buffer.concat([encrypted, cipher.final()]);
    
    // Get auth tag and append
    const authTag = cipher.getAuthTag();
    const encryptedWithTag = Buffer.concat([encrypted, authTag]);

    // Wrap AES key with RSA-OAEP
    const wrappedKey = crypto.publicEncrypt(
      {
        key: rsaPublicKeyPem,
        padding: crypto.constants.RSA_PKCS1_OAEP_PADDING,
        oaepHash: 'sha1'
      },
      aesKey
    );

    return {
      data: wrappedKey.toString('base64'),
      eventPayload: encryptedWithTag.toString('base64'),
      ivBase64Enc: iv.toString('base64')
    };
  }

  /**
   * Step 8: Submit payloads
   */
  async submitPayloads() {
    console.log('[6/9] Building and submitting payloads...');
    
    const fingerprint = this.generateFingerprint();
    const symbols = ['HxbgMbJ', 'XssGbLY']; // Two different symbols for two submissions

    for (let i = 0; i < 2; i++) {
      console.log(`  [${i + 1}/2] Preparing submission with symbol: ${symbols[i]}`);
      
      // Build payload to sign
      const payloadToSign = {
        symbolEntry: symbols[i],
        events: [JSON.stringify(fingerprint)],
        metrics: [],
        nonce: this.nonce
      };

      // Sign the payload
      const signature = this.signPayload(payloadToSign);

      // Add signature and tamper flag
      const totalPayload = {
        ...payloadToSign,
        signature: signature,
        preludeTamperedWith: false
      };

      // Encrypt the payload
      const encryptedPayload = this.encryptPayload(totalPayload);

      // Submit to server
      const response = await this.request(
        'POST',
        'https://apis.roblox.com/rotating-client-service/v1/submit',
        {
          userId: this.userId,
          challengeId: this.challengeId,
          payloadV2: encryptedPayload.eventPayload,
          params: {
            key: encryptedPayload.data,
            iv: encryptedPayload.ivBase64Enc
          }
        }
      );

      if (response.status !== 200) {
        throw new Error(`Submission ${i + 1} failed: ${JSON.stringify(response.body)}`);
      }

      console.log(`  Submission ${i + 1} successful`);
    }
  }

  /**
   * Step 9: Continue challenge
   */
  async continueChallenge() {
    console.log('[7/9] Continuing challenge flow...');
    
    const metadata = JSON.stringify({
      userId: this.userId,
      challengeId: this.challengeId,
      browserTrackerId: this.browserTrackerId
    });

    const response = await this.request(
      'POST',
      'https://apis.roblox.com/challenge/v1/continue',
      {
        challengeID: this.challengeId,
        challengeMetadata: metadata,
        challengeType: 'chef'
      }
    );

    console.log(`  Challenge continued (status: ${response.status})`);
    
    // Check if challenge completed or needs more steps
    if (response.body.challengeType) {
      console.log(`  Next challenge type: ${response.body.challengeType}`);
    } else {
      console.log(`  Challenge completed`);
    }
    
    // Store redemption metadata for final request
    if (response.body.redemptionToken) {
      this.redemptionToken = response.body.redemptionToken;
    }
    
    return response.body;
  }

  /**
   * Step 10: Retry original request with challenge redemption
   */
  async retryOriginalRequest(username) {
    console.log('[8/9] Retrying original request with challenge redemption...');
    
    // Build challenge redemption headers
    const challengeHeaders = {
      'rblx-challenge-id': this.challengeId,
      'rblx-challenge-type': 'chef',
      'rblx-challenge-metadata': Buffer.from(JSON.stringify({
        userId: this.userId,
        challengeId: this.challengeId,
        browserTrackerId: this.browserTrackerId,
        redemptionToken: this.redemptionToken
      })).toString('base64')
    };
    
    const response = await this.request(
      'POST',
      'https://auth.roblox.com/v1/username',
      { username },
      challengeHeaders
    );

    console.log(`  Final status: ${response.status}`);
    
    if (response.status === 200) {
      console.log('\nCHEF CHALLENGE COMPLETED SUCCESSFULLY');
      return response.body;
    } else if (response.status === 401 && response.headers['rblx-challenge-type']) {
      console.log('\nAdditional challenge triggered:', response.headers['rblx-challenge-type']);
      console.log('Response:', JSON.stringify(response.body, null, 2));
      return response.body;
    } else {
      console.log('\nRequest failed');
      console.log('Response:', JSON.stringify(response.body, null, 2));
      return response.body;
    }
  }
  
  /**
   * Step 11: Verify challenge completion
   */
  async verifyCompletion() {
    console.log('[9/9] Verifying challenge completion...');
    
    // Make a simple authenticated request to verify session is valid
    const response = await this.request(
      'GET',
      'https://users.roblox.com/v1/users/authenticated'
    );
    
    if (response.status === 200) {
      console.log(`  Session verified for user: ${response.body.name}`);
      return true;
    } else {
      console.log(`  Session verification failed (status: ${response.status})`);
      return false;
    }
  }

  /**
   * Helper: Generate nonce
   */
  generateNonce() {
    return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, (c) => {
      const r = Math.random() * 16 | 0;
      const v = c === 'x' ? r : (r & 0x3 | 0x8);
      return v.toString(16);
    });
  }

  /**
   * Main solver entry point
   */
  async solve(username) {
    try {
      console.log('\nROBLOX CHEF CHALLENGE SOLVER');
      console.log('================================\n');

      await this.getCsrfToken();
      await this.triggerChallenge(username);
      await this.generateKeyPair();
      await this.registerPublicKey();
      await this.fetchScripts();
      await this.submitPayloads();
      await this.continueChallenge();
      const result = await this.retryOriginalRequest(username);
      await this.verifyCompletion();

      console.log('\n================================');
      console.log('Solver execution complete\n');

      return result;
    } catch (error) {
      console.error('\nERROR:', error.message);
      console.error('Stack:', error.stack);
      throw error;
    }
  }
}

// Export for use
module.exports = ChefChallengeSolver;

// CLI usage
if (require.main === module) {
  const args = process.argv.slice(2);
  
  if (args.length < 2) {
    console.log('Usage: node chef_solver.js <username> <.ROBLOSECURITY_cookie>');
    console.log('Example: node chef_solver.js testuser "_|WARNING:-DO-NOT-SHARE-THIS.--Sharing..."');
    process.exit(1);
  }

  const [username, cookie] = args;
  const solver = new ChefChallengeSolver(`.ROBLOSECURITY=${cookie}`);
  
  solver.solve(username)
    .then(() => process.exit(0))
    .catch(() => process.exit(1));
}
