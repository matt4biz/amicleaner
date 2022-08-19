package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

func main() {
	region := "us-east-2"
	dryRun := true
	verbose := false

	flag.BoolVar(&dryRun, "dry-run", dryRun, "dry-run (list only)")
	flag.BoolVar(&verbose, "verbose", verbose, "verbose output")
	flag.StringVar(&region, "region", region, "AWS region to search")
	flag.Parse()

	// Using the SDK's default configuration, loading additional config
	// and credentials values from the environment variables, shared
	// credentials, and shared configuration files

	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion(region))

	if err != nil {
		log.Fatalf("unable to load SDK config, %v", err)
	}

	stsClient := sts.NewFromConfig(cfg)
	callerID, err := stsClient.GetCallerIdentity(context.TODO(), &sts.GetCallerIdentityInput{})

	if err != nil {
		log.Fatalf("retrieving account: %v\n", err)
	}

	accountID := *callerID.Account

	if verbose {
		fmt.Println("[searching account " + accountID + " in region " + region + "]\n")
	}

	ec2Client := ec2.NewFromConfig(cfg)
	idFilter := types.Filter{Name: aws.String("owner-id"), Values: []string{accountID}}

	// get the instances in order to get a list of AMIs they use

	instanceIn := ec2.DescribeInstancesInput{Filters: []types.Filter{idFilter}}
	instances, err := ec2Client.DescribeInstances(context.TODO(), &instanceIn)

	if err != nil {
		log.Fatalf("retrieving instances: %v\n", err)
	}

	knownAMIs := make(map[string][]string, len(instances.Reservations))
	knownVols := make(map[string]string)

	for _, r := range instances.Reservations {
		if verbose {
			fmt.Println("Reservation ID: " + *r.ReservationId)
			fmt.Println("Instance IDs:")
		}

		for _, i := range r.Instances {
			if verbose {
				fmt.Printf("   %s: ami = %s\n", *i.InstanceId, *i.ImageId)
			}

			usedVols := make([]string, 0, len(i.BlockDeviceMappings))

			for _, e := range i.BlockDeviceMappings {
				if verbose {
					fmt.Printf("     vol = %s\n", *e.Ebs.VolumeId)
				}

				usedVols = append(usedVols, *e.Ebs.VolumeId)
				knownVols[*e.Ebs.VolumeId] = *i.InstanceId
			}

			// states other than shutting down / terminated hold onto
			// the image (per Amazon, use only the low byte of Code)

			if state := *i.State.Code & 0xff; state != 32 && state != 48 {
				knownAMIs[*i.ImageId] = usedVols
			} else if verbose {
				fmt.Printf("   *** ignored, state = %s", i.State.Name)
			}
		}

		if verbose {
			fmt.Println()
		}
	}

	// add any AMIs which are tied to launch templates for an ASG

	profileIn := ec2.DescribeLaunchTemplateVersionsInput{Versions: []string{"$Latest", "$Default"}}
	profiles, err := ec2Client.DescribeLaunchTemplateVersions(context.TODO(), &profileIn)

	if err != nil {
		log.Fatalf("retrieving launch templates: %v\n", err)
	}

	for _, p := range profiles.LaunchTemplateVersions {
		imageID := *p.LaunchTemplateData.ImageId

		if verbose {
			fmt.Printf("Template ID: %s name = %s, image = %s\n", *p.LaunchTemplateId, *p.LaunchTemplateName, imageID)
		}

		knownAMIs[imageID] = append(knownAMIs[imageID], *p.LaunchTemplateName)
	}

	// now get all the AMIs in our account

	amiIn := ec2.DescribeImagesInput{Filters: []types.Filter{idFilter}}
	amis, err := ec2Client.DescribeImages(context.TODO(), &amiIn)

	if err != nil {
		log.Fatalf("retrieving AMIs: %v\n", err)
	}

	allAMIs := make(map[string]string, len(amis.Images))
	knownSnaps := make(map[string]string)

	for _, a := range amis.Images {
		if verbose {
			fmt.Printf("Image ID: %s name = %s\n", *a.ImageId, *a.Name)
		}

		allAMIs[*a.ImageId] = *a.Name

		for _, e := range a.BlockDeviceMappings {
			if verbose {
				fmt.Printf("     snap = %s\n", *e.Ebs.SnapshotId)
			}

			knownSnaps[*e.Ebs.SnapshotId] = *a.ImageId
		}

		if verbose {
			fmt.Println()
		}
	}

	// remove known AMIs from the list of all of them; what's
	// left are not in use

	for a := range knownAMIs {
		delete(allAMIs, a)
	}

	// and finally get all the snapshots

	snapIn := ec2.DescribeSnapshotsInput{OwnerIds: []string{accountID}}
	snaps, err := ec2Client.DescribeSnapshots(context.TODO(), &snapIn)

	if err != nil {
		log.Fatalf("retrieving snapshots: %v\n", err)
	}

	allSnaps := make(map[string]string, len(snaps.Snapshots))

	for _, s := range snaps.Snapshots {
		tags := make(map[string]string, len(s.Tags))

		for _, t := range s.Tags {
			tags[*t.Key] = *t.Value
		}

		if verbose {
			fmt.Printf("Snapshot ID: %s name = %s\n", *s.SnapshotId, *s.VolumeId)
			fmt.Printf("     tags = %v\n", tags)
		}

		allSnaps[*s.SnapshotId] = *s.VolumeId

		if verbose {
			fmt.Println()
		}
	}

	// remove known snapshots from the list of all of them;
	// what's left are not in use

	for s := range knownSnaps {
		delete(allSnaps, s)
	}

	if verbose || dryRun {
		fmt.Println("known AMIs", knownAMIs)
		fmt.Println("known vols", knownVols)
		fmt.Println("known snaps", knownSnaps)
		fmt.Println()
		fmt.Println("remaining AMIs", allAMIs)
		fmt.Println("remaining snaps", allSnaps)
		fmt.Println()
	}

	if dryRun {
		fmt.Println("dry run only -- nothing deleted")
		return
	}

	if len(allAMIs) == 0 && len(allSnaps) == 0 {
		fmt.Println("nothing to delete")
		return
	}

	for _, a := range allAMIs {
		_, err := ec2Client.DeregisterImage(context.TODO(), &ec2.DeregisterImageInput{ImageId: aws.String(a)})

		if err != nil {
			log.Println("failed to deregister AMI " + a)
		} else {
			fmt.Println("deregistered " + a)
		}
	}

	for _, s := range allSnaps {
		_, err := ec2Client.DeleteSnapshot(context.TODO(), &ec2.DeleteSnapshotInput{SnapshotId: aws.String(s)})

		if err != nil {
			log.Println("failed to delete snapshot " + s)
		} else {
			fmt.Println("deleted " + s)
		}
	}
}
