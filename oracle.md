# Oracle VM

Oracle gives out free VMs, so that's what I've been using. This is how to configure it:

Here’s how to set up your Oracle Cloud VM and SSH into it from your iPhone.

---

## **Step 1: Create a Virtual Machine (VM)**
1. **Log in** to [Oracle Cloud Console](https://cloud.oracle.com).
2. Go to **Compute** → **Instances**.
3. Click **"Create Instance"**.
4. Set these options:
    - **Name**: Whatever you want.
    - **Image**: Choose **Ubuntu** (or another Linux distro).
    - **Shape**: Pick **Ampere (Arm)** (4 cores, 24GB RAM) for free tier.
    - **Networking**: Keep defaults for now.
5. **SSH Key**:
    - Click **"Create SSH keys"** → Download and **save the private key**.
    - You'll need this to log in.

6. Click **"Create"** and wait for it to finish provisioning.

---

## **Step 2: Allow SSH Access**
By default, Oracle blocks SSH. You need to open port **22**.

1. Go to **Networking** → **Virtual Cloud Networks (VCN)**.
2. Find your VCN and open the **Subnet** inside it.
3. Click **Security Lists** → **Default Security List**.
4. Click **"Add Ingress Rule"**:
    - **Source CIDR**: `0.0.0.0/0` (allows access from anywhere)
    - **Protocol**: `TCP`
    - **Port Range**: `22`
    - **Save**

---

## **Step 3: Connect via SSH from iPhone**
1. **Install Termius** or **Blink Shell** from the App Store.
2. Add a new SSH connection:
    - **Host**: Your VM’s public IP (found in Oracle Cloud console under "Instance Details").
    - **Username**: `ubuntu` (or `opc` if using Oracle Linux).
    - **Private Key**: Import the SSH key you downloaded earlier.
3. **Connect** and you're in!

---

## **Step 4: Keep Sessions Alive**
Install `tmux` to keep scripts running even when you disconnect:
```bash
sudo apt update && sudo apt install tmux -y
tmux new -s mysession
```
Now you can:
- **Detach**: `Ctrl + B`, then `D`
- **Reconnect** later: `tmux attach -t mysession`

Done! Now you can SSH anytime and resume your work. 🚀

# Go

Here’s how to install Go on Oracle Linux:

## Remember to 1.22.0 with the latest version from [golang.org](https://golang.org/dl/)

### **Method 2: Install Latest Go Manually**
1. **Download the latest Go binary**
   ```sh
   curl -OL https://go.dev/dl/go1.24.1.linux-amd64.tar.gz
   ```
2. **Remove any existing Go installation**
   ```sh
   sudo rm -rf /usr/local/go
   ```
3. **Extract and move Go to `/usr/local`**
   ```sh
   sudo tar -C /usr/local -xzf go1.24.1.linux-amd64.tar.gz
   ```
4. **Set up Go environment variables**  
   Add this to your `~/.bashrc` or `~/.bash_profile`:
   ```sh
   echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
   source ~/.bashrc
   ```
5. **Verify Installation**
   ```sh
   go version
   ```

Now you're good to go! 🚀


# git

Here’s how to install Git on Oracle Linux:

### **Method 1: Install via DNF (Recommended)**
1. **Update your system**
   ```sh
   sudo dnf update -y
   ```
2. **Install Git**
   ```sh
   sudo dnf install -y git
   ```
3. **Verify Installation**
   ```sh
   git --version
   ```

Now you’re all set! 🎯